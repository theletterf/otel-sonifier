#!/bin/bash

# OpenTelemetry Sonifier - Build and Run Script
# This script builds the extension, otelgen, collector, and starts the system

set -e  # Exit on any error

echo "🔨 Building OpenTelemetry Sonifier..."

# Step 1: Build the load generator
echo "📦 Building otelgen load generator..."
cd otelgen
go build -o otelgen
cd ..
echo "✅ otelgen built successfully"

# Step 2: Build the collector using OCB
echo "🏗️  Building collector with OpenTelemetry Collector Builder (OCB)..."
$HOME/go/bin/builder --config=builder-config.yaml
echo "✅ Collector built successfully"

# Step 3: Kill any existing collector processes
echo "🧹 Cleaning up existing processes..."
pkill -f otelcol-sonifier || true

# Step 4: Start the collector
echo "🚀 Starting OpenTelemetry Collector with Sonifier Extension..."
./otelcol-sonifier/otelcol-sonifier --config=collector-config.yaml &

# Wait a moment for the collector to start
sleep 3

echo ""
echo "🎉 OpenTelemetry Sonifier is running!"
echo ""
echo "🌐 Web UI: http://localhost:44444"
echo "📡 OTLP gRPC: localhost:4317"
echo "📡 OTLP HTTP: localhost:4318"
echo ""
echo "💧 Ready for raindrop visualization!"
echo ""
echo "🔧 Try these commands in another terminal:"
echo "   ./otelgen low     # Gentle rain (0.2 traces/sec)"
echo "   ./otelgen medium  # Light rain (10 traces/sec)"
echo "   ./otelgen high    # Heavy rain (100 traces/sec)"
echo "   ./otelgen stress  # Storm (1000 traces/sec)"
echo ""
echo "🎨 Sky gradients will transition through:"
echo "   Low    → Night sky (deep blue)"
echo "   Medium → Dusk sky (purple)"
echo "   High   → Pre-dawn sky (reddish purple)"
echo "   Stress → Dawn sky (orange/red)"
echo ""
echo "🔊 Don't forget to enable audio in the web UI!"
echo ""
echo "Press Ctrl+C to stop the collector"

# Keep the script running until user stops it
wait