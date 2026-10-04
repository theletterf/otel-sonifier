package sonifierexporter

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Weather is the state sent to the web UI on every tick. It summarizes the
// telemetry seen recently and how far it is from what is normal for this
// system.
type Weather struct {
	// TickMs is the time between Weather messages.
	TickMs int64 `json:"tickMs"`

	// Rate is requests per second over the fast window. A request is a span
	// with no parent, or a server or consumer span.
	Rate float64 `json:"rate"`
	// ErrorRate is the fraction of requests with an error status.
	ErrorRate float64 `json:"errorRate"`
	// Latency percentiles of requests, in milliseconds.
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`

	// Baseline is what normal looks like, learned over the baseline window.
	Baseline Baseline `json:"baseline"`

	// Scores are 0 (calm) to 1 (stormy) per signal.
	Scores Scores `json:"scores"`
	// Mood combines the scores into one smoothed 0..1 value for the sky.
	Mood float64 `json:"mood"`

	// Logs counts log records by severity over the fast window, per second.
	Logs LogRates `json:"logs"`
	// Host holds the latest system utilization gauges (0..1), if any.
	Host *Host `json:"host,omitempty"`
	// Services are the busiest services over the fast window.
	Services []Service `json:"services"`

	// Drops is a uniform sample of the requests that arrived during the last
	// tick, for the UI to render as raindrops.
	Drops []Drop `json:"drops"`
}

type Baseline struct {
	Rate      float64 `json:"rate"`
	ErrorRate float64 `json:"errorRate"`
	P95       float64 `json:"p95"`
}

type Scores struct {
	// Load rises when traffic is well above baseline (8x is 1).
	Load float64 `json:"load"`
	// Quiet rises when traffic is well below baseline (1/8 is 1). It does not
	// count toward mood: silence is worth noticing but is not a storm.
	Quiet float64 `json:"quiet"`
	// Errors is absolute: an error rate is bad even if it is usual.
	Errors float64 `json:"errors"`
	// Latency rises when p95 is above its baseline (4x is 1).
	Latency float64 `json:"latency"`
	// Saturation rises as CPU or memory utilization goes above 70%.
	Saturation float64 `json:"saturation"`
}

type LogRates struct {
	Info  float64 `json:"info"`
	Warn  float64 `json:"warn"`
	Error float64 `json:"error"`
}

type Host struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}

type Service struct {
	Name      string  `json:"name"`
	Rate      float64 `json:"rate"`
	ErrorRate float64 `json:"errorRate"`
}

type Drop struct {
	TraceID string  `json:"id"`
	Error   bool    `json:"error"`
	Service string  `json:"service"`
	Ms      float64 `json:"ms"`
}

const (
	maxDurationsPerTick = 1024
	maxServices         = 8
	moodTimeConstant    = 3 * time.Second
	// hostStaleAfter drops host gauges that stopped being reported.
	hostStaleAfter = 30 * time.Second
)

// bucket accumulates telemetry for one tick.
type bucket struct {
	requests  int
	errors    int
	durations []float64 // reservoir sample, at most maxDurationsPerTick
	services  map[string]*serviceCount
	logs      [3]int // info, warn, error
}

type serviceCount struct{ requests, errors int }

func newBucket() *bucket {
	return &bucket{services: map[string]*serviceCount{}}
}

// weather aggregates telemetry into Weather states. Telemetry goes into the
// current bucket; tick closes the bucket and computes the state over the last
// few buckets (the fast window) and the long-running baseline.
type weather struct {
	tick        time.Duration
	windowTicks int
	baselineTau time.Duration
	maxDrops    int
	rng         *rand.Rand

	mu      sync.Mutex
	current *bucket
	drops   []Drop
	seen    int // requests seen this tick, for reservoir sampling of drops
	window  []*bucket

	host     Host
	hostSeen time.Time

	ticks         int // ticks so far, for baseline warm-up
	latencyTicks  int // ticks with latency data so far
	baseRate      float64
	baseErrorRate float64
	baseP95       float64
	mood          float64
}

func newWeather(cfg *Config) *weather {
	windowTicks := int(cfg.FastWindow / cfg.TickInterval)
	if windowTicks < 1 {
		windowTicks = 1
	}
	return &weather{
		tick:        cfg.TickInterval,
		windowTicks: windowTicks,
		baselineTau: cfg.BaselineWindow,
		maxDrops:    cfg.MaxDropsPerTick,
		rng:         rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		current:     newBucket(),
	}
}

func isRequest(span ptrace.Span) bool {
	return span.ParentSpanID().IsEmpty() ||
		span.Kind() == ptrace.SpanKindServer ||
		span.Kind() == ptrace.SpanKindConsumer
}

func serviceName(res pcommon.Resource) string {
	if v, ok := res.Attributes().Get("service.name"); ok {
		return v.AsString()
	}
	return "unknown"
}

func (w *weather) addTraces(td ptrace.Traces) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.current

	for _, rs := range td.ResourceSpans().All() {
		service := serviceName(rs.Resource())
		for _, ss := range rs.ScopeSpans().All() {
			for _, span := range ss.Spans().All() {
				if !isRequest(span) {
					continue
				}
				failed := span.Status().Code() == ptrace.StatusCodeError
				ms := 0.0
				if span.EndTimestamp() > span.StartTimestamp() {
					ms = float64(span.EndTimestamp()-span.StartTimestamp()) / 1e6
				}

				b.requests++
				sc := b.services[service]
				if sc == nil {
					sc = &serviceCount{}
					b.services[service] = sc
				}
				sc.requests++
				if failed {
					b.errors++
					sc.errors++
				}
				if ms > 0 {
					w.sampleDuration(b, ms)
				}

				// Reservoir sampling keeps a uniform sample, so the share of
				// red drops matches the error rate.
				w.seen++
				drop := Drop{TraceID: span.TraceID().String(), Error: failed, Service: service, Ms: ms}
				if len(w.drops) < w.maxDrops {
					w.drops = append(w.drops, drop)
				} else if j := w.rng.IntN(w.seen); j < w.maxDrops {
					w.drops[j] = drop
				}
			}
		}
	}
}

func (w *weather) sampleDuration(b *bucket, ms float64) {
	n := b.requests // includes this request
	if len(b.durations) < maxDurationsPerTick {
		b.durations = append(b.durations, ms)
	} else if j := w.rng.IntN(n); j < maxDurationsPerTick {
		b.durations[j] = ms
	}
}

func (w *weather) addLogs(ld plog.Logs) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.current

	for _, rl := range ld.ResourceLogs().All() {
		for _, sl := range rl.ScopeLogs().All() {
			for _, lr := range sl.LogRecords().All() {
				switch sev := lr.SeverityNumber(); {
				case sev >= plog.SeverityNumberError:
					b.logs[2]++
				case sev >= plog.SeverityNumberWarn:
					b.logs[1]++
				default:
					b.logs[0]++
				}
			}
		}
	}
}

func (w *weather) addMetrics(md pmetric.Metrics, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, rm := range md.ResourceMetrics().All() {
		for _, sm := range rm.ScopeMetrics().All() {
			for _, m := range sm.Metrics().All() {
				var target *float64
				switch m.Name() {
				case "system.cpu.utilization":
					target = &w.host.CPU
				case "system.memory.utilization":
					target = &w.host.Memory
				default:
					continue
				}
				if m.Type() != pmetric.MetricTypeGauge {
					continue
				}
				// Report the busiest data point (CPU core, host).
				peak, found := 0.0, false
				for _, dp := range m.Gauge().DataPoints().All() {
					v := dp.DoubleValue()
					if dp.ValueType() == pmetric.NumberDataPointValueTypeInt {
						v = float64(dp.IntValue())
					}
					if !found || v > peak {
						peak, found = v, true
					}
				}
				if found {
					*target = peak
					w.hostSeen = now
				}
			}
		}
	}
}

// advance closes the current tick and returns the new state.
func (w *weather) advance(now time.Time) Weather {
	w.mu.Lock()
	b, drops := w.current, w.drops
	w.current, w.drops, w.seen = newBucket(), nil, 0
	host, hostSeen := w.host, w.hostSeen
	w.mu.Unlock()

	// The window and baseline are only touched by the ticking goroutine.
	w.window = append(w.window, b)
	if len(w.window) > w.windowTicks {
		w.window = w.window[1:]
	}
	windowSec := float64(len(w.window)) * w.tick.Seconds()
	tickSec := w.tick.Seconds()

	var requests, errors int
	var logs [3]int
	var durations []float64
	services := map[string]*serviceCount{}
	for _, wb := range w.window {
		requests += wb.requests
		errors += wb.errors
		durations = append(durations, wb.durations...)
		for i := range logs {
			logs[i] += wb.logs[i]
		}
		for name, c := range wb.services {
			s := services[name]
			if s == nil {
				s = &serviceCount{}
				services[name] = s
			}
			s.requests += c.requests
			s.errors += c.errors
		}
	}

	st := Weather{
		TickMs: w.tick.Milliseconds(),
		Rate:   float64(requests) / windowSec,
		Logs: LogRates{
			Info:  float64(logs[0]) / windowSec,
			Warn:  float64(logs[1]) / windowSec,
			Error: float64(logs[2]) / windowSec,
		},
		Drops:    drops,
		Services: topServices(services, windowSec),
	}
	if st.Drops == nil {
		st.Drops = []Drop{}
	}
	if requests > 0 {
		st.ErrorRate = float64(errors) / float64(requests)
	}
	if len(durations) > 0 {
		sort.Float64s(durations)
		st.P50 = percentile(durations, 0.50)
		st.P95 = percentile(durations, 0.95)
	}
	if !hostSeen.IsZero() && now.Sub(hostSeen) < hostStaleAfter {
		st.Host = &host
	}

	w.updateBaseline(b, st.P95, tickSec)
	st.Baseline = Baseline{Rate: w.baseRate, ErrorRate: w.baseErrorRate, P95: w.baseP95}
	st.Scores = w.scores(st)

	target := max(st.Scores.Load, st.Scores.Errors, st.Scores.Latency, st.Scores.Saturation)
	w.mood += (target - w.mood) * (1 - math.Exp(-tickSec/moodTimeConstant.Seconds()))
	st.Mood = w.mood
	return st
}

// updateBaseline folds the latest tick into exponentially weighted averages.
// Until enough ticks have been seen, it uses a plain running mean, so the
// baseline is meaningful from the start instead of creeping up from zero.
func (w *weather) updateBaseline(b *bucket, p95, tickSec float64) {
	w.ticks++
	alpha := max(tickSec/w.baselineTau.Seconds(), 1/float64(w.ticks))
	w.baseRate += (float64(b.requests)/tickSec - w.baseRate) * alpha
	if b.requests > 0 {
		w.baseErrorRate += (float64(b.errors)/float64(b.requests) - w.baseErrorRate) * alpha
	}
	if p95 > 0 {
		w.latencyTicks++
		alpha := max(tickSec/w.baselineTau.Seconds(), 1/float64(w.latencyTicks))
		w.baseP95 += (p95 - w.baseP95) * alpha
	}
}

func (w *weather) scores(st Weather) Scores {
	// Adding 1 req/s to both sides keeps tiny rates from producing huge ratios.
	ratio := (st.Rate + 1) / (w.baseRate + 1)
	s := Scores{
		Load:   clamp01(math.Log2(ratio) / 3),
		Quiet:  clamp01(-math.Log2(ratio) / 3),
		Errors: clamp01((st.ErrorRate - 0.01) / 0.3),
	}
	if st.P95 > 0 && w.baseP95 > 0 {
		s.Latency = clamp01(math.Log2(st.P95/w.baseP95) / 2)
	}
	if st.Host != nil {
		s.Saturation = clamp01((max(st.Host.CPU, st.Host.Memory) - 0.7) / 0.25)
	}
	return s
}

func topServices(services map[string]*serviceCount, windowSec float64) []Service {
	out := make([]Service, 0, len(services))
	for name, c := range services {
		svc := Service{Name: name, Rate: float64(c.requests) / windowSec}
		if c.requests > 0 {
			svc.ErrorRate = float64(c.errors) / float64(c.requests)
		}
		out = append(out, svc)
	}
	slices.SortFunc(out, func(a, b Service) int {
		if a.Rate != b.Rate {
			if a.Rate > b.Rate {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		return 1
	})
	if len(out) > maxServices {
		out = out[:maxServices]
	}
	return out
}

// percentile expects sorted values.
func percentile(sorted []float64, p float64) float64 {
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(idx, len(sorted)-1))]
}

func clamp01(v float64) float64 {
	return max(0, min(1, v))
}
