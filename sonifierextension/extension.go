package sonifierextension

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.uber.org/zap"
)

//go:embed web
var webFiles embed.FS

type sonifierExtension struct {
	config        *Config
	logger        *zap.Logger
	server        *http.Server
	wg            sync.WaitGroup
	telemetryData *bytes.Buffer
	telemetryType string
	mu            sync.Mutex
	wsUpgrader    websocket.Upgrader
	wsConnections map[*websocket.Conn]bool
	wsConnMutex   sync.Mutex
}

func newSonifierExtension(config *Config, logger *zap.Logger) *sonifierExtension {
	return &sonifierExtension{
		config:        config,
		logger:        logger,
		telemetryData: &bytes.Buffer{},
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
		wsConnections: make(map[*websocket.Conn]bool),
	}
}

func (s *sonifierExtension) Start(_ context.Context, host component.Host) error {
	s.logger.Info("Starting sonifier extension server", zap.String("endpoint", s.config.Endpoint))

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", s.handleTelemetry)
	mux.HandleFunc("/v1/metrics", s.handleTelemetry) 
	mux.HandleFunc("/v1/logs", s.handleTelemetry)
	mux.HandleFunc("/telemetry", s.handleTelemetry) // Legacy endpoint
	mux.HandleFunc("/telemetry-data", s.handleGetTelemetryData)
	
	// Serve embedded web files
	s.logger.Info("Setting up embedded web files")
	
	// List files in the embedded filesystem for debugging
	fs.WalkDir(webFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			s.logger.Info("Found embedded file", zap.String("path", path))
		}
		return nil
	})
	
	webFS, fsErr := fs.Sub(webFiles, "web")
	if fsErr != nil {
		s.logger.Error("Failed to create web filesystem", zap.Error(fsErr))
		return fsErr
	}
	s.logger.Info("Web filesystem created successfully")
	

	
	// Set up WebSocket route
	mux.HandleFunc("/ws", s.handleWebSocket)
	
	// Main visualization
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	s.logger.Info("Setting up HTTP listener", zap.String("endpoint", s.config.Endpoint))
	
	// Create listener first
	ln, err := s.config.ServerConfig.ToListener(context.Background())
	if err != nil {
		s.logger.Error("Failed to create listener", zap.Error(err))
		return err
	}
	
	// Create server
	s.server, err = s.config.ServerConfig.ToServer(context.Background(), host, component.TelemetrySettings{Logger: s.logger}, nil)
	if err != nil {
		s.logger.Error("Failed to create HTTP server", zap.Error(err))
		return err
	}
	
	// Set the handler
	s.server.Handler = mux
	s.logger.Info("HTTP server created successfully", zap.String("address", ln.Addr().String()))

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.logger.Info("Starting HTTP server", zap.String("address", ln.Addr().String()))
		if err := s.server.Serve(ln); err != http.ErrServerClosed {
			s.logger.Error("Server error", zap.Error(err))
		} else {
			s.logger.Info("HTTP server stopped gracefully")
		}
	}()

	return nil
}

func (s *sonifierExtension) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down sonifier extension server")
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}
	s.wg.Wait()
	return nil
}

func (s *sonifierExtension) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var dataType string
	var jsonData []byte
	
	// Check if it's already JSON by looking for known OTLP JSON structures
	if json.Valid(body) {
		var jsonObj map[string]interface{}
		if json.Unmarshal(body, &jsonObj) == nil {
			if _, hasResourceSpans := jsonObj["resourceSpans"]; hasResourceSpans {
				dataType = "traces"
				jsonData = body
			} else if _, hasResourceMetrics := jsonObj["resourceMetrics"]; hasResourceMetrics {
				dataType = "metrics"
				jsonData = body
			} else if _, hasResourceLogs := jsonObj["resourceLogs"]; hasResourceLogs {
				dataType = "logs"
				jsonData = body
			} else {
				dataType = "unknown"
				jsonData = body
			}
		} else {
			dataType = "unknown"
			jsonData = body
		}
	} else {
		// Try to parse as protobuf
		if tracesReq := ptraceotlp.NewExportRequest(); tracesReq.UnmarshalProto(body) == nil {
			dataType = "traces"
			if jsonBytes, err := tracesReq.MarshalJSON(); err == nil {
				jsonData = jsonBytes
			}
		} else if metricsReq := pmetricotlp.NewExportRequest(); metricsReq.UnmarshalProto(body) == nil {
			dataType = "metrics"
			if jsonBytes, err := metricsReq.MarshalJSON(); err == nil {
				jsonData = jsonBytes
			}
		} else if logsReq := plogotlp.NewExportRequest(); logsReq.UnmarshalProto(body) == nil {
			dataType = "logs"
			if jsonBytes, err := logsReq.MarshalJSON(); err == nil {
				jsonData = jsonBytes
			}
		} else {
			dataType = "unknown"
			jsonData = body // fallback to raw data
		}
	}

	s.mu.Lock()
	s.telemetryData.Reset()
	if len(jsonData) > 0 {
		s.telemetryData.Write(jsonData)
	} else {
		s.telemetryData.Write(body)
	}
	s.telemetryType = dataType
	
	// Prepare message for WebSocket broadcast
	var payload json.RawMessage
	data := s.telemetryData.Bytes()
	if json.Valid(data) {
		payload = json.RawMessage(data)
	} else {
		jsonStr, _ := json.Marshal(string(data))
		payload = json.RawMessage(jsonStr)
	}

	response := struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}{
		Type:    dataType,
		Payload: payload,
	}

	messageBytes, err := json.Marshal(response)
	if err == nil {
		// Broadcast immediately to all WebSocket connections
		s.broadcastToWebSockets(messageBytes)
	}
	
	s.mu.Unlock()

	s.logger.Info("Received telemetry data", zap.String("type", dataType))
	w.WriteHeader(http.StatusOK)
}

func (s *sonifierExtension) handleGetTelemetryData(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.telemetryData.Len() == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Validate that the payload is valid JSON
	var payload json.RawMessage
	data := s.telemetryData.Bytes()
	
	// Check if data is valid JSON
	if json.Valid(data) {
		payload = json.RawMessage(data)
	} else {
		// If not valid JSON, encode it as a string
		jsonStr, _ := json.Marshal(string(data))
		payload = json.RawMessage(jsonStr)
	}

	response := struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}{
		Type:    s.telemetryType,
		Payload: payload,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to write telemetry data response", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}



func (s *sonifierExtension) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("Failed to upgrade WebSocket connection", zap.Error(err))
		return
	}

	// Add connection to the map
	s.wsConnMutex.Lock()
	s.wsConnections[conn] = true
	s.wsConnMutex.Unlock()

	s.logger.Info("WebSocket connection established")

	// Handle connection cleanup
	defer func() {
		s.wsConnMutex.Lock()
		delete(s.wsConnections, conn)
		s.wsConnMutex.Unlock()
		conn.Close()
		s.logger.Info("WebSocket connection closed")
	}()

	// Keep connection alive and handle messages
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.Error("WebSocket error", zap.Error(err))
			}
			break
		}
	}
}

func (s *sonifierExtension) broadcastToWebSockets(message []byte) {
	s.wsConnMutex.Lock()
	defer s.wsConnMutex.Unlock()

	for conn := range s.wsConnections {
		err := conn.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			s.logger.Error("Failed to write to WebSocket", zap.Error(err))
			conn.Close()
			delete(s.wsConnections, conn)
		}
	}
}
