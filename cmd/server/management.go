package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// ManagementServer provides HTTP endpoints for runtime server management
type ManagementServer struct {
	connManager *ConnectionManager
	port        string
	server      *http.Server
	mu          sync.RWMutex
}

// NewManagementServer creates a new management server
func NewManagementServer(connManager *ConnectionManager, port string) *ManagementServer {
	ms := &ManagementServer{
		connManager: connManager,
		port:        port,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", ms.healthHandler)
	mux.HandleFunc("/stats", ms.statsHandler)
	mux.HandleFunc("/limit", ms.limitHandler)
	mux.HandleFunc("/limit/increase", ms.increaseLimitHandler)
	mux.HandleFunc("/limit/decrease", ms.decreaseLimitHandler)

	ms.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	return ms
}

// Start starts the management server
func (ms *ManagementServer) Start() error {
	fmt.Printf("Management server started on port %s\n", ms.port)
	return ms.server.ListenAndServe()
}

// Stop stops the management server
func (ms *ManagementServer) Stop() error {
	return ms.server.Close()
}

// healthHandler returns server health status
func (ms *ManagementServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"status":  "healthy",
		"service": "shibudb",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// statsHandler returns connection statistics
func (ms *ManagementServer) statsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := ms.connManager.GetConnectionStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// limitHandler handles GET (current limit) and PUT (update limit) requests
func (ms *ManagementServer) limitHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		// Return current limit
		response := map[string]interface{}{
			"current_limit":      ms.connManager.GetMaxConnections(),
			"active_connections": ms.connManager.GetActiveConnections(),
		}
		json.NewEncoder(w).Encode(response)

	case http.MethodPut:
		// Update limit
		var request struct {
			Limit int32 `json:"limit"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := ms.connManager.UpdateLimit(request.Limit); err != nil {
			response := map[string]interface{}{
				"error":  err.Error(),
				"status": "failed",
			}
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(response)
			return
		}

		response := map[string]interface{}{
			"status":    "success",
			"new_limit": request.Limit,
			"message":   fmt.Sprintf("Connection limit updated to %d", request.Limit),
		}
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// increaseLimitHandler increases the connection limit by a specified amount
func (ms *ManagementServer) increaseLimitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Amount int32 `json:"amount"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		// Default to 100 if no amount specified
		request.Amount = 100
	}

	currentLimit := ms.connManager.GetMaxConnections()
	newLimit := currentLimit + request.Amount

	if err := ms.connManager.UpdateLimit(newLimit); err != nil {
		response := map[string]interface{}{
			"error":  err.Error(),
			"status": "failed",
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	response := map[string]interface{}{
		"status":          "success",
		"old_limit":       currentLimit,
		"new_limit":       newLimit,
		"increase_amount": request.Amount,
		"message":         fmt.Sprintf("Connection limit increased from %d to %d", currentLimit, newLimit),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// decreaseLimitHandler decreases the connection limit by a specified amount
func (ms *ManagementServer) decreaseLimitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Amount int32 `json:"amount"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		// Default to 100 if no amount specified
		request.Amount = 100
	}

	currentLimit := ms.connManager.GetMaxConnections()
	activeConnections := ms.connManager.GetActiveConnections()
	newLimit := currentLimit - request.Amount

	// Ensure we don't go below active connections
	if newLimit < activeConnections {
		response := map[string]interface{}{
			"error":              fmt.Sprintf("Cannot decrease limit to %d when %d connections are active", newLimit, activeConnections),
			"status":             "failed",
			"current_limit":      currentLimit,
			"active_connections": activeConnections,
			"minimum_allowed":    activeConnections,
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	if err := ms.connManager.UpdateLimit(newLimit); err != nil {
		response := map[string]interface{}{
			"error":  err.Error(),
			"status": "failed",
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	response := map[string]interface{}{
		"status":          "success",
		"old_limit":       currentLimit,
		"new_limit":       newLimit,
		"decrease_amount": request.Amount,
		"message":         fmt.Sprintf("Connection limit decreased from %d to %d", currentLimit, newLimit),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
