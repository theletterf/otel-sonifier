package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
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

	rootCmd.AddCommand(lowCmd, mediumCmd, highCmd, stressCmd)

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

	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
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
	defer traceExporter.Shutdown(ctx)

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(config.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create metric exporter: %w", err)
	}
	defer metricExporter.Shutdown(ctx)

	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(config.Endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create log exporter: %w", err)
	}
	defer logExporter.Shutdown(ctx)

	// Setup providers with immediate export (no batching)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithBatchTimeout(1*time.Millisecond),  // Export immediately
			sdktrace.WithMaxExportBatchSize(1),             // One trace at a time
			sdktrace.WithExportTimeout(100*time.Millisecond),
		),
		sdktrace.WithResource(res),
	)
	defer tp.Shutdown(ctx)
	otel.SetTracerProvider(tp)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(2*time.Second), // Export metrics every 2 seconds
		)),
		sdkmetric.WithResource(res),
	)
	defer mp.Shutdown(ctx)
	otel.SetMeterProvider(mp)

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	defer lp.Shutdown(ctx)

	// Create telemetry instruments
	tracer := otel.Tracer("otelgen")
	meter := otel.Meter("otelgen")
	logger := lp.Logger("otelgen")

	// Create metrics
	cpuGauge, _ := meter.Float64Gauge("system.cpu.utilization")
	memoryGauge, _ := meter.Float64Gauge("system.memory.utilization")
	diskCounter, _ := meter.Int64Counter("system.disk.io")
	httpCounter, _ := meter.Int64Counter("http.server.requests")

	// Start generators
	done := make(chan struct{})
	
	// Trace generator
	go generateTraces(ctx, tracer, config, done)
	
	// Metric generator  
	go generateMetrics(ctx, cpuGauge, memoryGauge, diskCounter, httpCounter, config, done)
	
	// Log generator
	go generateLogs(ctx, logger, config, done)

	<-ctx.Done()
	close(done)

	fmt.Printf("✅ Activity simulation completed\n")
	return nil
}

func generateTraces(ctx context.Context, tracer trace.Tracer, config Config, done <-chan struct{}) {
	operations := []string{
		"GET /api/users/{id}",
		"POST /api/orders", 
		"GET /api/products",
		"PUT /api/users/{id}",
		"DELETE /api/sessions/{id}",
		"GET /api/health",
		"POST /api/auth/login",
		"GET /api/metrics",
	}

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		default:
			operation := operations[rand.Intn(len(operations))]
			
			_, span := tracer.Start(ctx, operation)
			
			// Add attributes based on operation
			spaceIdx := strings.Index(operation, " ")
			method := operation[:spaceIdx]
			route := operation[spaceIdx+1:]
			
			span.SetAttributes(
				attribute.String("http.method", method),
				attribute.String("http.route", route),
				attribute.String("user.id", fmt.Sprintf("user_%d", rand.Intn(1000))),
				attribute.Int("http.status_code", getStatusCode(config.ErrorRate)),
			)
			
			// Simulate processing time
			processingTime := time.Duration(rand.Intn(200)) * time.Millisecond
			time.Sleep(processingTime)
			
			// Set span status based on error rate
			if rand.Float64() < config.ErrorRate {
				span.RecordError(fmt.Errorf("%s failed", operation))
				span.SetStatus(codes.Error, "Request failed")
			} else {
				span.SetStatus(codes.Ok, "")
			}
			
			span.End()
			
			// Random delay before next trace - much more natural
			randomDelay := time.Duration(rand.Float64() * float64(config.TraceRate) * 2)
			time.Sleep(randomDelay)
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
					attribute.String("status", fmt.Sprintf("%d", getStatusCode(config.ErrorRate)))))
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

func getStatusCode(errorRate float64) int {
	if rand.Float64() < errorRate {
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