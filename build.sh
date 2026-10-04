#!/bin/bash
# Builds the otelgen load generator and the collector with the Sonifier exporter.

set -e

BUILDER_VERSION=v0.162.0
GOBIN=$(go env GOBIN)
BUILDER="${GOBIN:-$(go env GOPATH)/bin}/builder"

if [ ! -x "$BUILDER" ] || ! "$BUILDER" version 2>/dev/null | grep -q "${BUILDER_VERSION#v}"; then
    echo "Installing OpenTelemetry Collector Builder $BUILDER_VERSION..."
    go install go.opentelemetry.io/collector/cmd/builder@$BUILDER_VERSION
fi

echo "Building otelgen load generator..."
(cd otelgen && go build -o otelgen)

echo "Building collector..."
"$BUILDER" --config=builder-config.yaml

echo "✅ Built ./otelcol-sonifier/otelcol-sonifier and ./otelgen/otelgen"
