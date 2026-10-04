package sonifierexporter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// startAll creates and starts the traces, metrics, and logs exporters for a
// single config, as the collector does when one exporter is in three pipelines.
func startAll(t *testing.T) (*hub, exporter.Traces, exporter.Metrics, exporter.Logs) {
	t.Helper()
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	cfg.NetAddr.Endpoint = "127.0.0.1:0"
	require.NoError(t, cfg.Validate())

	ctx := context.Background()
	set := exportertest.NewNopSettings(f.Type())
	te, err := f.CreateTraces(ctx, set, cfg)
	require.NoError(t, err)
	me, err := f.CreateMetrics(ctx, set, cfg)
	require.NoError(t, err)
	le, err := f.CreateLogs(ctx, set, cfg)
	require.NoError(t, err)

	host := componenttest.NewNopHost()
	require.NoError(t, te.Start(ctx, host))
	require.NoError(t, me.Start(ctx, host))
	require.NoError(t, le.Start(ctx, host))

	h := getHub(cfg, set)
	t.Cleanup(func() {
		require.NoError(t, te.Shutdown(ctx))
		require.NoError(t, me.Shutdown(ctx))
		require.NoError(t, le.Shutdown(ctx))
		hubsMu.Lock()
		defer hubsMu.Unlock()
		assert.NotContains(t, hubs, cfg, "hub should be released after the last shutdown")
	})
	return h, te, me, le
}

func dial(t *testing.T, h *hub) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+h.addr.String()+"/ws", nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	// Registration happens in the server goroutine after the handshake.
	require.Eventually(t, func() bool {
		h.clientsMu.Lock()
		defer h.clientsMu.Unlock()
		return len(h.clients) > 0
	}, time.Second, 5*time.Millisecond)
	return conn
}

func readEnvelope(t *testing.T, conn *websocket.Conn) envelope {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	var env envelope
	require.NoError(t, json.Unmarshal(data, &env))
	return env
}

func TestSharedServerServesUI(t *testing.T) {
	h, _, _, _ := startAll(t)

	resp, err := http.Get("http://" + h.addr.String() + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "script.js")
}

func TestBroadcastsAllSignals(t *testing.T) {
	h, te, me, le := startAll(t)
	conn := dial(t, h)
	ctx := context.Background()

	td := ptrace.NewTraces()
	span := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetTraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	span.SetName("checkout")
	span.Status().SetCode(ptrace.StatusCodeError)
	require.NoError(t, te.ConsumeTraces(ctx, td))

	env := readEnvelope(t, conn)
	assert.Equal(t, "traces", env.Type)
	gotTraces, err := (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(env.Payload)
	require.NoError(t, err)
	gotSpan := gotTraces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "checkout", gotSpan.Name())
	assert.Equal(t, ptrace.StatusCodeError, gotSpan.Status().Code())

	md := pmetric.NewMetrics()
	g := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	g.SetName("system.cpu.utilization")
	g.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(0.3)
	require.NoError(t, me.ConsumeMetrics(ctx, md))
	env = readEnvelope(t, conn)
	assert.Equal(t, "metrics", env.Type)
	assert.Contains(t, string(env.Payload), "system.cpu.utilization")

	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().SetSeverityText("ERROR")
	require.NoError(t, le.ConsumeLogs(ctx, ld))
	env = readEnvelope(t, conn)
	assert.Equal(t, "logs", env.Type)
	assert.Contains(t, string(env.Payload), "ERROR")
}

func TestSlowClientDoesNotBlockPipeline(t *testing.T) {
	h, te, _, _ := startAll(t)
	_ = dial(t, h) // connected but never reads

	td := ptrace.NewTraces()
	spans := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	for range 500 {
		spans.AppendEmpty().SetName("padding-to-fill-socket-buffers")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2000 {
			_ = te.ConsumeTraces(context.Background(), td)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ConsumeTraces blocked on a client that is not reading")
	}
}
