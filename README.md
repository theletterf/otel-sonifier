# OTel Sonifier



A concept extension for the OpenTelemetry Collector that implements monitoring based on Calm Technology principles. Instead of traditional dashboards and alerts, OTel Sonifier creates a peripheral awareness system using rain and sky hue shifts that respond to telemetry in the background of our attention.

<a href="https://www.youtube.com/watch?v=q3H-TQLEKnw" target="_blank"><img width="1098" height="681" alt="sonifier" src="https://github.com/user-attachments/assets/965d5335-8ae5-4bf7-99ef-621a746e3ae4" /></a>

The goal is to make monitoring feel as natural as checking the weather: something we do without thinking, that provides immediate understanding of our environment.

## Philosophy

OTel Sonifier embodies the principles of Calm Technology by making system health visible without demanding focus. Like weather patterns that we notice subconsciously, the extension transforms telemetry data into environmental changes:

- **Rain patterns** represent trace activity: gentle drizzle for normal operations, intense downpour for high load. Rain drops are trace IDs.
- **Sky gradients** shift from deep blue (healthy) through purple and red (increasing stress) to orange (critical).
- **Audio feedback** provides subtle raindrop sounds that sync with trace impacts.

The goal is to create a monitoring experience that feels more like observing nature than managing infrastructure.

## Quick start

Run the automated build and setup script:

```bash
./build-and-run.sh
```

This will:

1. Build the otelgen load generator.
2. Build the collector using OpenTelemetry Collector Builder (OCB).
3. Start the collector with sonifier extension.
4. Display the web UI link and usage examples.

### Run

Start the collector with the provided configuration:

```bash
./otelcol-sonifier/otelcol-sonifier --config=collector-config.yaml
```

The collector will start with:

- OTLP receivers on ports 4317 (gRPC) and 4318 (HTTP)
- Sonifier extension web UI on http://localhost:44444
- Real-time raindrop visualization of trace IDs
- Audio feedback system with ground impact sounds
- Smooth sky gradient transitions between load levels

## Usage

Generate telemetry at different activity levels:

```bash
# Low activity (30s): 25 traces/sec, 10% constant metrics, 5% errors
./otelgen low

# Medium activity (60s): 67 traces/sec, 30% constant metrics, 15% errors  
./otelgen medium

# High activity (90s): 125 traces/sec, 60% constant metrics, 35% errors
./otelgen high

# Stress testing (120s): 1000 traces/sec, 100% constant metrics, 50% errors
./otelgen stress
```

## File structure

```
otel-sonifier/
├── collector-config.yaml          # Main collector configuration
├── otelcol-sonifier               # Built collector binary
├── sonifierextension/             # Custom extension source
│   ├── extension.go              # Main extension logic
│   ├── config.go                 # Extension configuration
│   ├── factory.go                # Extension factory
│   └── web/                      # Web UI and visualization system
│       ├── index.html            # Main web interface
│       ├── script.js             # Main visualization logic and controls
│       ├── style.css             # Styling and UI controls
│       ├── rain-engine.js        # Simple raindrop sound effects
│       └── telemetry-analyzer.js # Telemetry processing for visualization
├── otelgen/                      # Load generator
│   ├── main.go                   # Generator implementation
│   ├── go.mod                    # Go dependencies
│   └── otelgen                   # Built generator binary
└── README.md                     # This documentation
```

## Ideas for future development

### How to represent cluster activity?

The current system focuses on individual traces, but cluster-level monitoring could introduce:

- **Cloud formations**: Different cloud types representing cluster health states.
- **Wind patterns**: Air currents showing inter-service communication flows.
- **Seasonal changes**: Long-term trends manifesting as weather seasons.
- **Geographic features**: Mountains and valleys representing resource utilization.

### Should logs be represented?

Logs currently influence the overall atmosphere, but could be more directly visualized:

- **Lightning strikes**: Error logs as brief, bright flashes across the sky.
- **Thunder**: Warning logs as distant rumbles.
- **Fog**: Info logs as atmospheric moisture that affects visibility.
- **Storms**: Critical logs as weather fronts that change the entire environment.

### Create generative ambience music in real time?

The current audio system is minimal, but could evolve into:

- **Weather-based soundscapes**: Rain intensity affecting background music tempo.
- **Harmonic progression**: System health influencing chord structures.
- **Instrument selection**: Different telemetry types choosing different instruments.
- **Rhythmic patterns**: Trace frequency creating drum patterns.
- **Mood modulation**: Error rates shifting musical modes from major to minor.

## License

This project is licensed under the Apache License, Version 2.0. Refer to the [LICENSE](LICENSE) file for details.
