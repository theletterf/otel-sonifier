package sonifierexporter

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

//go:embed web
var webFiles embed.FS

const writeTimeout = 5 * time.Second

// hub serves the web UI and fans telemetry out to every connected browser.
// One hub is shared by the traces, metrics, and logs exporters of a single
// sonifier config.
type hub struct {
	cfg      *Config
	settings component.TelemetrySettings
	release  func()

	lifecycleMu sync.Mutex
	refs        int
	server      *http.Server
	addr        net.Addr
	serveDone   chan struct{}

	upgrader websocket.Upgrader

	clientsMu sync.Mutex
	clients   map[*client]struct{}

	tracesMarshaler  ptrace.JSONMarshaler
	metricsMarshaler pmetric.JSONMarshaler
	logsMarshaler    plog.JSONMarshaler
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

// envelope is the message format the web UI expects: the signal type plus the
// OTLP/JSON payload.
type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func newHub(cfg *Config, settings component.TelemetrySettings, release func()) *hub {
	return &hub{
		cfg:      cfg,
		settings: settings,
		release:  release,
		clients:  map[*client]struct{}{},
	}
}

// Start starts the web server the first time any signal's exporter starts.
func (h *hub) Start(ctx context.Context, host component.Host) error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	h.refs++
	if h.refs > 1 {
		return nil
	}

	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		h.refs--
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWebSocket)
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	ln, err := h.cfg.ToListener(ctx)
	if err != nil {
		h.refs--
		return err
	}

	h.server, err = h.cfg.ToServer(ctx, host.GetExtensions(), h.settings, mux)
	if err != nil {
		ln.Close()
		h.refs--
		return err
	}
	h.addr = ln.Addr()
	h.serveDone = make(chan struct{})

	h.settings.Logger.Info("Sonifier web UI listening", zap.String("address", "http://"+h.addr.String()))

	go func() {
		defer close(h.serveDone)
		if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.settings.Logger.Error("Sonifier web server stopped", zap.Error(err))
		}
	}()
	return nil
}

// Shutdown stops the web server once every signal's exporter has shut down.
func (h *hub) Shutdown(ctx context.Context) error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	if h.refs == 0 {
		return nil
	}
	h.refs--
	if h.refs > 0 {
		return nil
	}
	h.release()

	// http.Server.Shutdown does not close hijacked WebSocket connections.
	h.clientsMu.Lock()
	for c := range h.clients {
		c.conn.Close()
	}
	h.clientsMu.Unlock()

	err := h.server.Shutdown(ctx)
	<-h.serveDone
	return err
}

func (h *hub) pushTraces(_ context.Context, td ptrace.Traces) error {
	payload, err := h.tracesMarshaler.MarshalTraces(td)
	if err != nil {
		return err
	}
	return h.broadcast("traces", payload)
}

func (h *hub) pushMetrics(_ context.Context, md pmetric.Metrics) error {
	payload, err := h.metricsMarshaler.MarshalMetrics(md)
	if err != nil {
		return err
	}
	return h.broadcast("metrics", payload)
}

func (h *hub) pushLogs(_ context.Context, ld plog.Logs) error {
	payload, err := h.logsMarshaler.MarshalLogs(ld)
	if err != nil {
		return err
	}
	return h.broadcast("logs", payload)
}

// broadcast queues a message for every client without blocking. A client whose
// buffer is full misses the message: the UI is ambient, so dropping data for
// a slow tab is better than applying backpressure to the pipeline.
func (h *hub) broadcast(signal string, payload []byte) error {
	msg, err := json.Marshal(envelope{Type: signal, Payload: payload})
	if err != nil {
		return err
	}

	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			h.settings.Logger.Debug("Dropping message for slow WebSocket client", zap.String("signal", signal))
		}
	}
	return nil
}

func (h *hub) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.settings.Logger.Debug("WebSocket upgrade failed", zap.Error(err))
		return
	}
	// Clear any deadlines inherited from the HTTP server's timeouts.
	_ = conn.SetReadDeadline(time.Time{})

	c := &client{conn: conn, send: make(chan []byte, h.cfg.ClientBufferSize)}
	h.clientsMu.Lock()
	h.clients[c] = struct{}{}
	h.clientsMu.Unlock()
	h.settings.Logger.Info("WebSocket client connected", zap.String("remote", r.RemoteAddr))

	done := make(chan struct{})
	go h.writeLoop(c, done)

	// The UI never sends anything; reading only detects disconnects.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	h.clientsMu.Lock()
	delete(h.clients, c)
	h.clientsMu.Unlock()
	close(done)
	conn.Close()
	h.settings.Logger.Info("WebSocket client disconnected", zap.String("remote", r.RemoteAddr))
}

func (h *hub) writeLoop(c *client, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case msg := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				// Closing the connection makes the read loop exit and clean up.
				c.conn.Close()
				return
			}
		}
	}
}
