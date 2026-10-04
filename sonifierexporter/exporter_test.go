package sonifierexporter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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
	cfg.TickInterval = 20 * time.Millisecond
	cfg.FastWindow = 100 * time.Millisecond
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

// readWeather reads weather messages until one satisfies ok.
func readWeather(t *testing.T, conn *websocket.Conn, ok func(Weather) bool) Weather {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		env := readEnvelope(t, conn)
		require.Equal(t, "weather", env.Type)
		var st Weather
		require.NoError(t, json.Unmarshal(env.Payload, &st))
		if ok(st) {
			return st
		}
	}
	t.Fatal("no matching weather message")
	return Weather{}
}

func TestSendsWeatherFromAllSignals(t *testing.T) {
	h, te, me, le := startAll(t)
	conn := dial(t, h)
	ctx := context.Background()

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetTraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	span.Status().SetCode(ptrace.StatusCodeError)
	require.NoError(t, te.ConsumeTraces(ctx, td))

	md := pmetric.NewMetrics()
	g := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	g.SetName("system.cpu.utilization")
	g.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(0.3)
	require.NoError(t, me.ConsumeMetrics(ctx, md))

	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().SetSeverityNumber(plog.SeverityNumberError)
	require.NoError(t, le.ConsumeLogs(ctx, ld))

	st := readWeather(t, conn, func(st Weather) bool { return len(st.Drops) > 0 })
	assert.Equal(t, []Drop{{TraceID: "0102030405060708090a0b0c0d0e0f10", Error: true, Service: "checkout"}}, st.Drops)
	assert.Equal(t, 1.0, st.ErrorRate)
	assert.Positive(t, st.Logs.Error)
	require.NotNil(t, st.Host)
	assert.Equal(t, 0.3, st.Host.CPU)

	resp, err := http.Get("http://" + h.addr.String() + "/debug")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var debug Weather
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&debug))
}

func TestSlowClientDoesNotBlockBroadcast(t *testing.T) {
	h, _, _, _ := startAll(t)
	_ = dial(t, h) // connected but never reads

	payload, err := json.Marshal(strings.Repeat("x", 100_000))
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2000 {
			_ = h.broadcast("weather", payload)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("broadcast blocked on a client that is not reading")
	}
}
