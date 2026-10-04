package sonifierexporter

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confighttp"
)

// Config has the configuration for the sonifier exporter.
type Config struct {
	// ServerConfig configures the HTTP server that hosts the web UI and the
	// WebSocket stream the UI listens to.
	confighttp.ServerConfig `mapstructure:",squash"`

	// ClientBufferSize is the number of messages buffered per WebSocket
	// client. When a client falls behind, newer messages are dropped for that
	// client so a slow browser tab never blocks the pipeline.
	ClientBufferSize int `mapstructure:"client_buffer_size"`

	// TickInterval is how often a weather update is sent to the UI.
	TickInterval time.Duration `mapstructure:"tick_interval"`

	// FastWindow is the period current conditions are measured over.
	FastWindow time.Duration `mapstructure:"fast_window"`

	// BaselineWindow is the time constant of the learned baseline: roughly
	// how long a change has to last before it becomes the new normal.
	BaselineWindow time.Duration `mapstructure:"baseline_window"`

	// MaxDropsPerTick caps how many sampled traces are sent per update.
	MaxDropsPerTick int `mapstructure:"max_drops_per_tick"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the exporter configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.NetAddr.Endpoint == "" {
		return errors.New("endpoint must be set")
	}
	if cfg.ClientBufferSize <= 0 {
		return errors.New("client_buffer_size must be positive")
	}
	if cfg.TickInterval <= 0 {
		return errors.New("tick_interval must be positive")
	}
	if cfg.FastWindow < cfg.TickInterval {
		return errors.New("fast_window must be at least tick_interval")
	}
	if cfg.BaselineWindow < cfg.FastWindow {
		return errors.New("baseline_window must be at least fast_window")
	}
	if cfg.MaxDropsPerTick < 0 {
		return errors.New("max_drops_per_tick must not be negative")
	}
	return nil
}
