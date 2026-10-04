package sonifierexporter

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

const (
	// typeStr is the type of the exporter.
	typeStr   = "sonifier"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a factory for the sonifier exporter.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		exporter.WithTraces(createTraces, stability),
		exporter.WithMetrics(createMetrics, stability),
		exporter.WithLogs(createLogs, stability),
	)
}

func createDefaultConfig() component.Config {
	server := confighttp.NewDefaultServerConfig()
	server.NetAddr.Endpoint = "localhost:44444"
	return &Config{
		ServerConfig:     server,
		ClientBufferSize: 256,
	}
}

// The same exporter configured in the traces, metrics, and logs pipelines is
// created once per signal. All of them must share a single web server, so
// hubs are keyed by config, the same way the OTLP receiver shares its server.
var (
	hubsMu sync.Mutex
	hubs   = map[*Config]*hub{}
)

func getHub(cfg *Config, set exporter.Settings) *hub {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	h, ok := hubs[cfg]
	if !ok {
		h = newHub(cfg, set.TelemetrySettings, func() {
			hubsMu.Lock()
			delete(hubs, cfg)
			hubsMu.Unlock()
		})
		hubs[cfg] = h
	}
	return h
}

func helperOptions(h *hub) []exporterhelper.Option {
	return []exporterhelper.Option{
		exporterhelper.WithStart(h.Start),
		exporterhelper.WithShutdown(h.Shutdown),
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
	}
}

func createTraces(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
	h := getHub(cfg.(*Config), set)
	return exporterhelper.NewTraces(ctx, set, cfg, h.pushTraces, helperOptions(h)...)
}

func createMetrics(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
	h := getHub(cfg.(*Config), set)
	return exporterhelper.NewMetrics(ctx, set, cfg, h.pushMetrics, helperOptions(h)...)
}

func createLogs(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
	h := getHub(cfg.(*Config), set)
	return exporterhelper.NewLogs(ctx, set, cfg, h.pushLogs, helperOptions(h)...)
}
