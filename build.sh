#!/bin/bash

set -e

# Set GOPATH if not set
export GOPATH=${GOPATH:-$(go env GOPATH)}
BUILDER_PATH="$GOPATH/bin/builder"

# Install builder if not already in the path
if ! command -v builder &> /dev/null; then
    if [ ! -f "$BUILDER_PATH" ]; then
        echo "Builder not found, installing v0.131.0..."
        go install go.opentelemetry.io/collector/cmd/builder@v0.131.0
    fi
else
    BUILDER_PATH=$(command -v builder)
fi

# Verify builder is available
if [ ! -f "$BUILDER_PATH" ]; then
    echo "Error: Builder installation failed or not found at $BUILDER_PATH"
    exit 1
fi

# Build otelgen load generator
echo "Building otelgen load generator..."
cd otelgen
go build -o otelgen
cd ..
echo "✅ otelgen built successfully"

# Build the collector
echo "Building custom collector..."
"$BUILDER_PATH" --config builder-config.yaml

# Run the collector
echo "Running custom collector..."
./otelcol-sonifier/otelcol-sonifier --config collector-config.yaml
