#!/bin/bash
# Builds everything, then runs the collector with the Sonifier exporter.

set -e

"$(dirname "$0")/build.sh"

echo ""
echo "🌐 Web UI:    http://localhost:44444"
echo "📡 OTLP gRPC: localhost:4317"
echo "📡 OTLP HTTP: localhost:4318"
echo ""
echo "In another terminal, generate telemetry:"
echo "   ./otelgen/otelgen low     # Drizzle"
echo "   ./otelgen/otelgen medium  # Light rain"
echo "   ./otelgen/otelgen high    # Heavy rain"
echo "   ./otelgen/otelgen stress  # Storm"
echo ""
echo "Enable audio in the web UI to hear the rain. Press Ctrl+C to stop."
echo ""

exec ./otelcol-sonifier/otelcol-sonifier --config=collector-config.yaml
