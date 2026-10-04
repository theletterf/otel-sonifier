package sonifierexporter

import (
	"errors"

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
	return nil
}
