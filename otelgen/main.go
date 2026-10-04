package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Duration     time.Duration
	TraceRate    time.Duration
	MetricRate   time.Duration
	LogRate      time.Duration
	ErrorRate    float64
	HighSeverity float64
	MaxCPU       float64
	MaxMemory    float64
	MaxDiskIO    float64
	Endpoint     string
	Insecure     bool
}

var (
	lowConfig = Config{
		Duration:     30 * time.Second,
		TraceRate:    5000 * time.Millisecond, // 0.2 traces/sec (just a handful)
		MetricRate:   5 * time.Second,  
		LogRate:      3 * time.Second,
		ErrorRate:    0.05,
		HighSeverity: 0.1,
		MaxCPU:       10.0,  // Constant 10%
		MaxMemory:    10.0,  // Constant 10%
		MaxDiskIO:    10.0,  // Constant 10%
		Endpoint:     "localhost:4317",
		Insecure:     true,
	}
	
	mediumConfig = Config{
		Duration:     60 * time.Second,
		TraceRate:    100 * time.Millisecond,  // 10 traces/sec 
		MetricRate:   2 * time.Second,
		LogRate:      1 * time.Second,
		ErrorRate:    0.15,
		HighSeverity: 0.3,
		MaxCPU:       30.0,  // Constant 30%
		MaxMemory:    30.0,  // Constant 30%
		MaxDiskIO:    30.0,  // Constant 30%
		Endpoint:     "localhost:4317",
		Insecure:     true,
	}
	
	highConfig = Config{
		Duration:     90 * time.Second,
		TraceRate:    10 * time.Millisecond,   // 100 traces/sec
		MetricRate:   500 * time.Millisecond,
		LogRate:      200 * time.Millisecond,
		ErrorRate:    0.35,
		HighSeverity: 0.6,
		MaxCPU:       60.0,  // Constant 60%
		MaxMemory:    60.0,  // Constant 60%
		MaxDiskIO:    60.0,  // Constant 60%
		Endpoint:     "localhost:4317",
		Insecure:     true,
	}

	stressConfig = Config{
		Duration:     120 * time.Second,
		TraceRate:    1 * time.Millisecond,    // 1000 traces/sec (maximum)
		MetricRate:   500 * time.Millisecond,
		LogRate:      100 * time.Millisecond,
		ErrorRate:    0.5,
		HighSeverity: 0.8,
		MaxCPU:       100.0, // Constant 100%
		MaxMemory:    100.0, // Constant 100%
		MaxDiskIO:    100.0, // Constant 100%
		Endpoint:     "localhost:4317",
		Insecure:     true,
	}
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "otelgen",
		Short: "Generate OpenTelemetry data at various load levels",
		Long:  "A utility to generate traces, metrics, and logs for system stress testing",
	}

	lowCmd := &cobra.Command{
		Use:   "low",
		Short: "Generate low activity telemetry data",
		RunE:  func(cmd *cobra.Command, args []string) error { return runGenerator(lowConfig) },
	}

	mediumCmd := &cobra.Command{
		Use:   "medium", 
		Short: "Generate medium activity telemetry data",
		RunE:  func(cmd *cobra.Command, args []string) error { return runGenerator(mediumConfig) },
	}

	highCmd := &cobra.Command{
		Use:   "high",
		Short: "Generate high activity telemetry data", 
		RunE:  func(cmd *cobra.Command, args []string) error { return runGenerator(highConfig) },
	}

	stressCmd := &cobra.Command{
		Use:   "stress",
		Short: "Generate stress-level telemetry data with 10x more traces",
		RunE:  func(cmd *cobra.Command, args []string) error { return runGenerator(stressConfig) },
	}

	waveCmd := &cobra.Command{
		Use:   "wave",
		Short: "Smooth sine wave from 0% to 100% over 2min, repeating until Ctrl+C",
		RunE:  func(cmd *cobra.Command, args []string) error { return runWaveGenerator() },
	}

	rootCmd.AddCommand(lowCmd, mediumCmd, highCmd, stressCmd, waveCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runGenerator(config Config) error {
	fmt.Printf("🚀 Starting %s activity simulation for %v\n", 
		getConfigName(config), config.Duration)
	fmt.Printf("📊 Trace rate: %v, Metric rate: %v, Log rate: %v\n", 
		config.TraceRate, config.MetricRate, config.LogRate)
	fmt.Printf("⚠️  Error rate: %.0f%%, High severity: %.0f%%\n", 
		config.ErrorRate*100, config.HighSeverity*100)

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(sigCtx, config.Duration)
	defer cancel()

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("otelgen"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("load.level", getConfigName(config)),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Setup exporters
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(config.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create trace exporter: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(config.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create metric exporter: %w", err)
	}

	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(config.Endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create log exporter: %w", err)
	}

	fleet, mp, lp := newProviders(res, traceExporter, metricExporter, logExporter, 2*time.Second)
	defer shutdownProviders(fleet, mp, lp)
	otel.SetMeterProvider(mp)

	// Create telemetry instruments
	meter := otel.Meter("otelgen")
	logger := lp.Logger("otelgen")

	// Create metrics
	cpuGauge, _ := meter.Float64Gauge("system.cpu.utilization")
	memoryGauge, _ := meter.Float64Gauge("system.memory.utilization")
	diskCounter, _ := meter.Int64Counter("system.disk.io")
	httpCounter, _ := meter.Int64Counter("http.server.requests")

	// Start generators
	done := make(chan struct{})
	start := time.Now()

	// Trace generator
	traces := generateTraces(ctx, fleet,
		func() time.Duration { return config.TraceRate },
		func() float64 { return config.ErrorRate })

	// Metric generator
	go generateMetrics(ctx, cpuGauge, memoryGauge, diskCounter, httpCounter, config, done)

	// Log generator
	go generateLogs(ctx, logger, config, done)

	<-ctx.Done()
	close(done)
	sent := traces.wait()

	elapsed := time.Since(start)
	fmt.Printf("✅ Activity simulation completed: %d traces in %v (%.1f/sec)\n",
		sent, elapsed.Round(time.Second), float64(sent)/elapsed.Seconds())
	return nil
}

// simService is one of the simulated services traces come from.
type simService struct {
	name string
	// share is the fraction of requests this service handles.
	share float64
	// errorFactor scales the error rate for this service. Shares times
	// factors sum to 1, so the overall error rate matches the level.
	errorFactor float64

	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// The simulated system: most traffic hits the frontend, and most errors come
// from payments, as in a typical incident.
var serviceMix = []simService{
	{name: "frontend", share: 0.60, errorFactor: 0.5},
	{name: "checkout", share: 0.25, errorFactor: 1.0},
	{name: "payments", share: 0.15, errorFactor: 3.0},
}

type fleet struct {
	services []*simService
	exporter sdktrace.SpanExporter
}

func (f *fleet) pick() *simService {
	r := rand.Float64()
	for _, s := range f.services {
		if r < s.share {
			return s
		}
		r -= s.share
	}
	return f.services[len(f.services)-1]
}

// sharedExporter lets several tracer providers use one exporter. Each
// provider shuts down its exporter; the real exporter is shut down once,
// after all providers have flushed.
type sharedExporter struct{ sdktrace.SpanExporter }

func (sharedExporter) Shutdown(context.Context) error { return nil }

// newProviders sets up the SDK providers, with one tracer provider per
// simulated service. Spans are exported in batches every 100ms: one RPC per
// span cannot keep up with the higher load levels.
func newProviders(res *resource.Resource, te sdktrace.SpanExporter, me sdkmetric.Exporter,
	le sdklog.Exporter, metricInterval time.Duration) (*fleet, *sdkmetric.MeterProvider, *sdklog.LoggerProvider) {
	f := &fleet{exporter: te}
	for _, mix := range serviceMix {
		svc := mix
		svcRes, err := resource.Merge(res, resource.NewSchemaless(semconv.ServiceName(svc.name)))
		if err != nil {
			svcRes = res
		}
		svc.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(sharedExporter{te},
				sdktrace.WithBatchTimeout(100*time.Millisecond),
				sdktrace.WithMaxQueueSize(20000),
			),
			sdktrace.WithResource(svcRes),
		)
		svc.tracer = svc.tp.Tracer("otelgen")
		f.services = append(f.services, &svc)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(metricInterval))),
		sdkmetric.WithResource(res),
	)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(le)),
		sdklog.WithResource(res),
	)
	return f, mp, lp
}

// shutdownProviders flushes and stops the providers (and their exporters).
// It uses a fresh context because the run's context is already done by now.
func shutdownProviders(f *fleet, mp *sdkmetric.MeterProvider, lp *sdklog.LoggerProvider) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range f.services {
		_ = s.tp.Shutdown(ctx)
	}
	_ = f.exporter.Shutdown(ctx)
	_ = mp.Shutdown(ctx)
	_ = lp.Shutdown(ctx)
}

var operations = []string{
	"GET /api/users/{id}",
	"POST /api/orders",
	"GET /api/products",
	"PUT /api/users/{id}",
	"DELETE /api/sessions/{id}",
	"GET /api/health",
	"POST /api/auth/login",
	"GET /api/metrics",
}

// traceRun tracks the simulated requests started by generateTraces.
type traceRun struct {
	wg   sync.WaitGroup
	sent atomic.Int64
	done chan struct{}
}

// wait blocks until the generator has stopped and every in-flight request
// has ended, and returns the number of traces sent.
func (r *traceRun) wait() int64 {
	<-r.done
	r.wg.Wait()
	return r.sent.Load()
}

// generateTraces starts simulated requests at an average rate of one per
// interval() until ctx is done. Each request runs in its own goroutine, so
// simulated processing time does not limit the rate.
func generateTraces(ctx context.Context, f *fleet, interval func() time.Duration, errorRate func() float64) *traceRun {
	run := &traceRun{done: make(chan struct{})}
	go func() {
		defer close(run.done)
		pace(ctx, interval, func() {
			run.wg.Add(1)
			run.sent.Add(1)
			go func() {
				defer run.wg.Done()
				simulateRequest(f.pick(), errorRate())
			}()
		})
	}()
	return run
}

func simulateRequest(svc *simService, errorRate float64) {
	operation := operations[rand.Intn(len(operations))]

	// Not derived from the run's context, so requests in flight at the end
	// still finish and get exported.
	_, span := svc.tracer.Start(context.Background(), operation)

	spaceIdx := strings.Index(operation, " ")
	failed := rand.Float64() < min(1, errorRate*svc.errorFactor)
	span.SetAttributes(
		attribute.String("http.method", operation[:spaceIdx]),
		attribute.String("http.route", operation[spaceIdx+1:]),
		attribute.String("user.id", fmt.Sprintf("user_%d", rand.Intn(1000))),
		attribute.Int("http.status_code", getStatusCode(failed)),
	)

	// Simulate processing time
	time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)

	if failed {
		span.RecordError(fmt.Errorf("%s failed", operation))
		span.SetStatus(codes.Error, "Request failed")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

// pace calls emit at an average rate of one call per interval() until ctx is
// done. It wakes every 10ms and emits however many calls are due, so it can
// reach rates beyond what time.Sleep resolves, and it picks up changes to
// interval() (as in wave mode) within one tick. Jitter keeps arrivals uneven,
// like real traffic.
func pace(ctx context.Context, interval func() time.Duration, emit func()) {
	const tick = 10 * time.Millisecond
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	due := rand.Float64() // start partway through the first interval
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			due += float64(tick) / float64(interval()) * (0.5 + rand.Float64())
			for ; due >= 1; due-- {
				emit()
			}
		}
	}
}

func generateMetrics(ctx context.Context, cpuGauge, memoryGauge metric.Float64Gauge, 
	diskCounter, httpCounter metric.Int64Counter, config Config, done <-chan struct{}) {
	ticker := time.NewTicker(config.MetricRate)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Generate constant metrics based on config level
			cpuUtil := config.MaxCPU / 100.0  // Convert percentage to decimal
			memUtil := config.MaxMemory / 100.0  // Convert percentage to decimal
			
			cpuGauge.Record(ctx, cpuUtil, 
				metric.WithAttributes(attribute.String("host", "app-server-01")))
			memoryGauge.Record(ctx, memUtil,
				metric.WithAttributes(attribute.String("host", "app-server-01")))
			
			// Disk I/O and HTTP requests based on constant level
			diskCounter.Add(ctx, int64(config.MaxDiskIO*10.24), // Scale to reasonable values
				metric.WithAttributes(attribute.String("device", "/dev/sda1")))
			httpCounter.Add(ctx, int64(rand.Intn(10)+1),
				metric.WithAttributes(
					attribute.String("method", "GET"),
					attribute.String("status", fmt.Sprintf("%d", getStatusCode(rand.Float64() < config.ErrorRate)))))
		}
	}
}

func generateLogs(ctx context.Context, logger log.Logger, config Config, done <-chan struct{}) {
	ticker := time.NewTicker(config.LogRate)
	defer ticker.Stop()

	messages := map[log.Severity][]string{
		log.SeverityInfo: {
			"User authentication successful",
			"Database connection established", 
			"Cache hit for user profile",
			"Background job completed",
			"Health check passed",
		},
		log.SeverityWarn: {
			"Cache miss for key: user_profile_123",
			"API rate limit approaching", 
			"Memory usage above 80%",
			"Slow database query detected",
		},
		log.SeverityError: {
			"Database connection failed",
			"Authentication failed for user",
			"Service timeout occurred", 
			"Disk space critically low",
		},
		log.SeverityFatal: {
			"Critical system failure",
			"Out of memory error",
			"Database corruption detected",
		},
	}

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			severity := getSeverity(config.HighSeverity)
			severityMessages := messages[severity]
			message := severityMessages[rand.Intn(len(severityMessages))]
			
			record := log.Record{}
			record.SetTimestamp(time.Now())
			record.SetBody(log.StringValue(message))
			record.SetSeverity(severity)
			record.AddAttributes(
				log.String("component", "api-server"),
				log.String("user.id", fmt.Sprintf("user_%d", rand.Intn(1000))),
				log.Int64("request.id", int64(rand.Intn(100000))),
			)
			
			logger.Emit(ctx, record)
		}
	}
}

// waveLevel stores the current wave level (0-1000 representing 0.0-1.0)
// shared between the wave ticker and trace/log generators
var waveLevel atomic.Int64

func getWaveLevel() float64 {
	return float64(waveLevel.Load()) / 1000.0
}

func runWaveGenerator() error {
	fmt.Println("Starting wave mode: smooth 0%->100%->0% over 2min, repeating until Ctrl+C")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("otelgen"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("load.level", "Wave"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("localhost:4317"),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create trace exporter: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint("localhost:4317"),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create metric exporter: %w", err)
	}

	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint("localhost:4317"),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create log exporter: %w", err)
	}

	fleet, mp, lp := newProviders(res, traceExporter, metricExporter, logExporter, 1*time.Second)
	defer shutdownProviders(fleet, mp, lp)
	otel.SetMeterProvider(mp)

	meter := otel.Meter("otelgen")
	logger := lp.Logger("otelgen")

	cpuGauge, _ := meter.Float64Gauge("system.cpu.utilization")
	memoryGauge, _ := meter.Float64Gauge("system.memory.utilization")
	diskCounter, _ := meter.Int64Counter("system.disk.io")

	startTime := time.Now()
	cycleDuration := 120.0 // 2 minutes

	done := make(chan struct{})

	// Wave-aware trace generator: rate follows the level, from one trace
	// every 5s at 0% to 1000 traces/sec at 100% (exponential)
	traces := generateTraces(ctx, fleet,
		func() time.Duration {
			return time.Duration(5000 * math.Pow(0.0002, getWaveLevel()) * float64(time.Millisecond))
		},
		func() float64 { return 0.02 + getWaveLevel()*0.48 })

	// Wave-aware log generator: rate and severity follow the level
	go generateWaveLogs(ctx, logger, done)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(done)
			sent := traces.wait()
			elapsed := time.Since(startTime)
			fmt.Printf("\nWave stopped: %d traces in %v (%.1f/sec)\n",
				sent, elapsed.Round(time.Second), float64(sent)/elapsed.Seconds())
			return nil
		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			progress := math.Mod(elapsed, cycleDuration) / cycleDuration
			level := (math.Sin(2*math.Pi*progress-math.Pi/2) + 1) / 2

			// Publish level for trace/log generators
			waveLevel.Store(int64(level * 1000))

			// Record metrics at current level
			cpuGauge.Record(ctx, level,
				metric.WithAttributes(attribute.String("host", "app-server-01")))
			memoryGauge.Record(ctx, level,
				metric.WithAttributes(attribute.String("host", "app-server-01")))
			diskCounter.Add(ctx, int64(level*1024),
				metric.WithAttributes(attribute.String("device", "/dev/sda1")))

			bar := int(level * 40)
			fmt.Printf("\r  Level: %5.1f%% [%s%s]", level*100,
				strings.Repeat("#", bar), strings.Repeat(".", 40-bar))
		}
	}
}

func generateWaveLogs(ctx context.Context, logger log.Logger, done <-chan struct{}) {
	messages := map[log.Severity][]string{
		log.SeverityInfo:  {"Health check passed", "Cache hit", "Request completed", "Connection established"},
		log.SeverityWarn:  {"Rate limit approaching", "Slow query detected", "Memory usage high"},
		log.SeverityError: {"Connection failed", "Timeout occurred", "Disk space low"},
		log.SeverityFatal: {"Critical failure", "Out of memory"},
	}

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		default:
			level := getWaveLevel()

			// Rate: 5s at level=0 → 100ms at level=1
			baseMs := 5000.0 * math.Pow(0.02, level)
			time.Sleep(time.Duration(baseMs) * time.Millisecond)

			severity := getSeverity(level * 0.8)
			severityMessages := messages[severity]
			message := severityMessages[rand.Intn(len(severityMessages))]

			record := log.Record{}
			record.SetTimestamp(time.Now())
			record.SetBody(log.StringValue(message))
			record.SetSeverity(severity)
			record.AddAttributes(
				log.String("component", "api-server"),
				log.String("user.id", fmt.Sprintf("user_%d", rand.Intn(1000))),
			)
			logger.Emit(ctx, record)
		}
	}
}

func getStatusCode(failed bool) int {
	if failed {
		codes := []int{400, 401, 403, 404, 500, 502, 503}
		return codes[rand.Intn(len(codes))]
	}
	codes := []int{200, 201, 202, 204}
	return codes[rand.Intn(len(codes))]
}

func getSeverity(highSeverityRate float64) log.Severity {
	if rand.Float64() < highSeverityRate {
		severities := []log.Severity{log.SeverityWarn, log.SeverityError, log.SeverityFatal}
		return severities[rand.Intn(len(severities))]
	}
	return log.SeverityInfo
}

func getConfigName(config Config) string {
	switch config.Duration {
	case 30 * time.Second:
		return "Low"
	case 60 * time.Second:
		return "Medium"  
	case 90 * time.Second:
		return "High"
	case 120 * time.Second:
		return "Stress"
	default:
		return "Custom"
	}
}