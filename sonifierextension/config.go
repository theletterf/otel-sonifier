package sonifierextension

import (
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/component"
)

// Config has the configuration for the sonifier extension.
type Config struct {
	confighttp.ServerConfig `mapstructure:",squash"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the extension configuration is valid
func (cfg *Config) Validate() error {
	return nil
}
