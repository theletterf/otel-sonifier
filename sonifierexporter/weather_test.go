package sonifierexporter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

var t0 = time.Unix(1_700_000_000, 0)

func testWeather(mutate func(*Config)) *weather {
	cfg := createDefaultConfig().(*Config)
	cfg.TickInterval = 200 * time.Millisecond
	cfg.FastWindow = time.Second
	cfg.BaselineWindow = 10 * time.Second
	if mutate != nil {
		mutate(cfg)
	}
	return newWeather(cfg)
}

// requests builds n root spans for a service, the first errors of which fail,
// each lasting ms milliseconds.
func requests(service string, n, errors int, ms float64) ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", service)
	spans := rs.ScopeSpans().AppendEmpty().Spans()
	start := pcommon.NewTimestampFromTime(t0)
	for i := range n {
		span := spans.AppendEmpty()
		span.SetTraceID(pcommon.TraceID{byte(i), byte(i >> 8), 1})
		span.SetStartTimestamp(start)
		span.SetEndTimestamp(start + pcommon.Timestamp(ms*1e6))
		if i < errors {
			span.Status().SetCode(ptrace.StatusCodeError)
		}
	}
	return td
}

// run advances the weather for n ticks, adding td on each, and returns the
// last state.
func run(w *weather, n int, td ptrace.Traces) Weather {
	var st Weather
	for i := range n {
		if td.SpanCount() > 0 {
			w.addTraces(td)
		}
		st = w.advance(t0.Add(time.Duration(i) * w.tick))
	}
	return st
}

func TestWeatherRatesAndLatency(t *testing.T) {
	w := testWeather(nil)
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	spans := rs.ScopeSpans().AppendEmpty().Spans()
	start := pcommon.NewTimestampFromTime(t0)
	for i := 1; i <= 10; i++ {
		span := spans.AppendEmpty()
		span.SetTraceID(pcommon.TraceID{byte(i)})
		span.SetStartTimestamp(start)
		span.SetEndTimestamp(start + pcommon.Timestamp(i*10*1e6)) // 10..100ms
		if i <= 2 {
			span.Status().SetCode(ptrace.StatusCodeError)
		}
	}
	w.addTraces(td)

	st := w.advance(t0)
	assert.InDelta(t, 50, st.Rate, 1e-9, "10 requests in one 200ms tick")
	assert.InDelta(t, 0.2, st.ErrorRate, 1e-9)
	assert.InDelta(t, 50, st.P50, 1e-9)
	assert.InDelta(t, 100, st.P95, 1e-9)
	require.Len(t, st.Drops, 10)
	assert.Equal(t, int64(200), st.TickMs)
	require.Len(t, st.Services, 1)
	assert.Equal(t, Service{Name: "checkout", Rate: 50, ErrorRate: 0.2}, st.Services[0])
}

func TestWeatherCountsOnlyRequestSpans(t *testing.T) {
	w := testWeather(nil)
	td := ptrace.NewTraces()
	spans := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	spans.AppendEmpty() // root: a request
	child := spans.AppendEmpty()
	child.SetParentSpanID(pcommon.SpanID{1})
	child.SetKind(ptrace.SpanKindClient) // outgoing call: not a request
	server := spans.AppendEmpty()
	server.SetParentSpanID(pcommon.SpanID{2})
	server.SetKind(ptrace.SpanKindServer) // incoming call: a request
	w.addTraces(td)

	st := w.advance(t0)
	assert.InDelta(t, 10, st.Rate, 1e-9, "2 requests in one 200ms tick")
	assert.Equal(t, "unknown", st.Services[0].Name)
}

func TestWeatherCapsDrops(t *testing.T) {
	w := testWeather(func(c *Config) { c.MaxDropsPerTick = 40 })
	w.addTraces(requests("api", 1000, 500, 5))
	st := w.advance(t0)
	require.Len(t, st.Drops, 40)

	errors := 0
	for _, d := range st.Drops {
		if d.Error {
			errors++
		}
	}
	// A uniform sample of a 50% error stream; very unlikely to be lopsided.
	assert.Greater(t, errors, 5)
	assert.Less(t, errors, 35)

	assert.Empty(t, w.advance(t0.Add(w.tick)).Drops, "drops are per tick")
}

func TestWeatherLoadIsRelativeToBaseline(t *testing.T) {
	w := testWeather(nil)
	steady := requests("api", 10, 0, 20)
	surge := requests("api", 100, 0, 20)

	st := run(w, 100, steady)
	assert.InDelta(t, 50, st.Baseline.Rate, 1, "baseline learns the steady rate")
	assert.Zero(t, st.Scores.Load)
	assert.InDelta(t, 0, st.Mood, 0.01)

	st = run(w, 5, surge)
	assert.Greater(t, st.Scores.Load, 0.8, "a 10x surge is stormy")
	assert.Greater(t, st.Mood, 0.0)
	assert.Less(t, st.Mood, st.Scores.Load, "mood follows scores gradually")

	st = run(w, 500, surge)
	assert.Less(t, st.Scores.Load, 0.1, "a sustained surge becomes the new normal")

	st = run(w, 10, ptrace.NewTraces())
	assert.Zero(t, st.Rate)
	assert.Greater(t, st.Scores.Quiet, 0.9, "sudden silence is noticed")
	assert.Zero(t, st.Scores.Load)
}

func TestWeatherErrorsAreAbsolute(t *testing.T) {
	w := testWeather(nil)
	st := run(w, 500, requests("api", 10, 5, 20))
	assert.InDelta(t, 0.5, st.Baseline.ErrorRate, 0.01)
	assert.Equal(t, 1.0, st.Scores.Errors, "a usual 50% error rate is still bad")
	assert.Greater(t, st.Mood, 0.95)

	st = run(w, 50, requests("api", 100, 0, 20))
	assert.Zero(t, st.Scores.Errors)
}

func TestWeatherLatencyIsRelativeToBaseline(t *testing.T) {
	w := testWeather(nil)
	st := run(w, 100, requests("api", 10, 0, 20))
	assert.InDelta(t, 20, st.Baseline.P95, 0.01)
	assert.Zero(t, st.Scores.Latency)

	st = run(w, 5, requests("api", 10, 0, 160))
	assert.Equal(t, 1.0, st.Scores.Latency, "8x slower is as bad as it gets")
}

func TestWeatherLogs(t *testing.T) {
	w := testWeather(nil)
	ld := plog.NewLogs()
	records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, sev := range []plog.SeverityNumber{
		plog.SeverityNumberUnspecified, plog.SeverityNumberInfo, plog.SeverityNumberWarn2,
		plog.SeverityNumberError, plog.SeverityNumberFatal,
	} {
		records.AppendEmpty().SetSeverityNumber(sev)
	}
	w.addLogs(ld)

	st := w.advance(t0)
	assert.Equal(t, LogRates{Info: 10, Warn: 5, Error: 10}, st.Logs, "per second over one 200ms tick")
}

func TestWeatherHostSaturation(t *testing.T) {
	w := testWeather(nil)
	st := w.advance(t0)
	assert.Nil(t, st.Host)

	md := pmetric.NewMetrics()
	ms := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics()
	cpu := ms.AppendEmpty()
	cpu.SetName("system.cpu.utilization")
	dps := cpu.SetEmptyGauge().DataPoints()
	dps.AppendEmpty().SetDoubleValue(0.4)
	dps.AppendEmpty().SetDoubleValue(0.95) // busiest core counts
	mem := ms.AppendEmpty()
	mem.SetName("system.memory.utilization")
	mem.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(0.5)
	w.addMetrics(md, t0)

	st = w.advance(t0)
	require.NotNil(t, st.Host)
	assert.Equal(t, Host{CPU: 0.95, Memory: 0.5}, *st.Host)
	assert.Equal(t, 1.0, st.Scores.Saturation)

	st = w.advance(t0.Add(time.Minute))
	assert.Nil(t, st.Host, "stale host metrics are dropped")
	assert.Zero(t, st.Scores.Saturation)
}

func TestWeatherTopServices(t *testing.T) {
	w := testWeather(nil)
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		w.addTraces(requests(name, i+1, 0, 1))
	}
	st := w.advance(t0)
	require.Len(t, st.Services, maxServices)
	assert.Equal(t, "j", st.Services[0].Name)
	assert.Equal(t, "c", st.Services[maxServices-1].Name)
}
