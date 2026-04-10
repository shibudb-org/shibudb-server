package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/auth"
	"github.com/shibudb.org/shibudb-server/internal/models"
	"github.com/shibudb.org/shibudb-server/internal/queryengine"
	"github.com/shibudb.org/shibudb-server/internal/spaces"
)

// ConnectionManager handles connection limiting and tracking
type ConnectionManager struct {
	maxConnections    int32
	activeConnections int32
	busyConnections   int32
	semaphore         chan struct{}
	connections       sync.Map
	mu                sync.RWMutex
	// Dynamic limit management
	limitUpdateChan chan int32
	shutdownChan    chan struct{}
	dataDir         string
}

type trackedConnection struct {
	conn net.Conn
	mu   sync.Mutex
	busy bool
}

// NewConnectionManager creates a new connection manager with the specified limit.
// dataDir is used to persist the connection limit across restarts.
func NewConnectionManager(maxConnections int32, dataDir string) *ConnectionManager {
	cm := &ConnectionManager{
		maxConnections:  maxConnections,
		semaphore:       make(chan struct{}, maxConnections),
		limitUpdateChan: make(chan int32, 10), // Buffer for limit updates
		shutdownChan:    make(chan struct{}),
		dataDir:         dataDir,
	}

	// Start the dynamic limit manager
	go cm.dynamicLimitManager()

	return cm
}

// dynamicLimitManager handles runtime limit updates
func (cm *ConnectionManager) dynamicLimitManager() {
	for {
		select {
		case newLimit := <-cm.limitUpdateChan:
			cm.updateLimit(newLimit)
		case <-cm.shutdownChan:
			return
		}
	}
}

// updateLimit safely updates the connection limit
func (cm *ConnectionManager) updateLimit(newLimit int32) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	oldLimit := cm.maxConnections
	cm.maxConnections = newLimit

	// Resize semaphore channel
	newSemaphore := make(chan struct{}, newLimit)

	// Transfer existing permits to new semaphore
	active := atomic.LoadInt32(&cm.activeConnections)
	for i := int32(0); i < active; i++ {
		select {
		case newSemaphore <- struct{}{}:
		default:
			// If new limit is smaller, we might not be able to transfer all
			break
		}
	}

	// Replace old semaphore
	cm.semaphore = newSemaphore

	fmt.Printf("Connection limit updated: %d -> %d (active: %d)\n", oldLimit, newLimit, active)

	// Save the new limit persistently
	if err := SaveConnectionLimit(cm.dataDir, newLimit); err != nil {
		fmt.Printf("Warning: Failed to save connection limit: %v\n", err)
	}
}

// UpdateLimit safely updates the connection limit at runtime
func (cm *ConnectionManager) UpdateLimit(newLimit int32) error {
	if newLimit <= 0 {
		return fmt.Errorf("connection limit must be positive")
	}

	// Check if new limit is smaller than current active connections
	active := atomic.LoadInt32(&cm.activeConnections)
	if newLimit < active {
		return fmt.Errorf("cannot set limit to %d when %d connections are active", newLimit, active)
	}

	select {
	case cm.limitUpdateChan <- newLimit:
		return nil
	default:
		return fmt.Errorf("limit update channel is full, try again later")
	}
}

// TryAcquire attempts to acquire a connection slot
func (cm *ConnectionManager) TryAcquire(conn net.Conn) bool {
	cm.mu.RLock()
	semaphore := cm.semaphore
	cm.mu.RUnlock()

	select {
	case semaphore <- struct{}{}:
		atomic.AddInt32(&cm.activeConnections, 1)
		cm.connections.Store(conn.RemoteAddr().String(), &trackedConnection{conn: conn})
		return true
	default:
		return false
	}
}

// Release releases a connection slot
func (cm *ConnectionManager) Release(conn net.Conn) {
	cm.mu.RLock()
	semaphore := cm.semaphore
	cm.mu.RUnlock()

	if value, ok := cm.connections.Load(conn.RemoteAddr().String()); ok {
		if tracked, ok := value.(*trackedConnection); ok {
			tracked.mu.Lock()
			if tracked.busy {
				tracked.busy = false
				atomic.AddInt32(&cm.busyConnections, -1)
			}
			tracked.mu.Unlock()
		}
	}

	<-semaphore
	atomic.AddInt32(&cm.activeConnections, -1)
	cm.connections.Delete(conn.RemoteAddr().String())
}

// GetActiveConnections returns the current number of active connections
func (cm *ConnectionManager) GetActiveConnections() int32 {
	return atomic.LoadInt32(&cm.activeConnections)
}

// GetMaxConnections returns the maximum allowed connections
func (cm *ConnectionManager) GetMaxConnections() int32 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.maxConnections
}

// GetConnectionStats returns detailed connection statistics
func (cm *ConnectionManager) GetConnectionStats() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	active := atomic.LoadInt32(&cm.activeConnections)
	busy := atomic.LoadInt32(&cm.busyConnections)
	max := cm.maxConnections
	idle := active - busy
	usage := float64(active) / float64(max) * 100

	return map[string]interface{}{
		"active_connections": active,
		"busy_connections":   busy,
		"idle_connections":   idle,
		"max_connections":    max,
		"usage_percentage":   usage,
		"available_slots":    max - active,
	}
}

// MarkConnectionBusy marks a connected client as currently processing work.
func (cm *ConnectionManager) MarkConnectionBusy(conn net.Conn) {
	value, ok := cm.connections.Load(conn.RemoteAddr().String())
	if !ok {
		return
	}
	tracked, ok := value.(*trackedConnection)
	if !ok {
		return
	}

	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if tracked.busy {
		return
	}

	tracked.busy = true
	atomic.AddInt32(&cm.busyConnections, 1)
}

// MarkConnectionIdle marks a connected client as no longer processing work.
func (cm *ConnectionManager) MarkConnectionIdle(conn net.Conn) {
	value, ok := cm.connections.Load(conn.RemoteAddr().String())
	if !ok {
		return
	}
	tracked, ok := value.(*trackedConnection)
	if !ok {
		return
	}

	tracked.mu.Lock()
	defer tracked.mu.Unlock()
	if !tracked.busy {
		return
	}

	tracked.busy = false
	atomic.AddInt32(&cm.busyConnections, -1)
}

// CloseAllConnections forcefully closes all active connections
func (cm *ConnectionManager) CloseAllConnections() {
	cm.connections.Range(func(key, value interface{}) bool {
		if tracked, ok := value.(*trackedConnection); ok {
			tracked.conn.Close()
		}
		return true
	})
}

// Shutdown gracefully shuts down the connection manager
func (cm *ConnectionManager) Shutdown() {
	close(cm.shutdownChan)
}

func StartServer(port string, authFilePath string, maxConnections int32, dataFolderPath string, managementPort string) {
	if port == managementPort {
		panic(fmt.Sprintf("client port and management port must differ (both are %s)", port))
	}
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)
	spaceRestoreStartedAt := time.Now()
	fmt.Printf("Loading spaces and indexes from %s before opening network listeners...\n", dataFolderPath)
	spaceManager := spaces.NewSpaceManager(dataFolderPath)
	fmt.Printf("Finished loading spaces in %s. Opening management and client listeners...\n", formatStartupDuration(time.Since(spaceRestoreStartedAt)))
	defer spaceManager.CloseAll()

	authManager, err := auth.NewAuthManager(authFilePath)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize auth: %v", err))
	}
	tokenManager, err := auth.NewTokenManager(filepath.Join(dataFolderPath, "management_tokens.json"))
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize token manager: %v", err))
	}

	// Load persistent connection limit if available
	persistentLimit := GetPersistentLimit(dataFolderPath, maxConnections)
	actualLimit := maxConnections
	if persistentLimit != maxConnections {
		fmt.Printf("Using persisted connection limit: %d (instead of %d)\n", persistentLimit, maxConnections)
		actualLimit = persistentLimit
	}

	// No connection_limit.json yet: persist the limit we are using (from CLI/default or from file above).
	if _, loadErr := LoadConnectionLimit(dataFolderPath); loadErr != nil && os.IsNotExist(loadErr) {
		if saveErr := SaveConnectionLimit(dataFolderPath, actualLimit); saveErr != nil {
			fmt.Printf("Warning: Failed to save connection limit: %v\n", saveErr)
		}
	}

	// Create connection manager
	connManager := NewConnectionManager(actualLimit, dataFolderPath)
	defer connManager.Shutdown()

	// Start connection monitoring goroutine
	go monitorConnections(connManager)

	// Start signal handler for runtime limit updates
	go handleSignals(connManager)

	managementServer := NewManagementServer(connManager, spaceManager, tokenManager, managementPort)
	go func() {
		fmt.Printf("Starting management server on port %s...\n", managementPort)
		if err := managementServer.Start(); err != nil {
			fmt.Printf("Management server error: %v\n", err)
		}
	}()

	// Give management server a moment to start
	time.Sleep(100 * time.Millisecond)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		panic(fmt.Sprintf("Failed to start server: %v", err))
	}
	defer listener.Close()

	fmt.Printf("ShibuDB server started on port %s (max connections: %d)\n", port, actualLimit)
	fmt.Printf("Management server started on port %s\n", managementPort)
	fmt.Printf("Runtime limit updates: SIGUSR1 (increase by 100), SIGUSR2 (decrease by 100)\n")
	fmt.Printf("HTTP management: GET/PUT http://localhost:%s/limit (Authorization: Bearer <token> required)\n", managementPort)

	// Show persistence status if different from default
	if actualLimit != maxConnections {
		fmt.Printf("Using persisted connection limit: %d (saved from previous session)\n", actualLimit)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Failed to accept client: %v\n", err)
			continue
		}

		// Check connection limit
		if !connManager.TryAcquire(conn) {
			fmt.Printf("Connection limit reached (%d/%d). Rejecting connection from %s\n",
				connManager.GetActiveConnections(), connManager.GetMaxConnections(), conn.RemoteAddr())

			// Send rejection message to client
			rejectionMsg := map[string]interface{}{
				"status":  "ERROR",
				"message": fmt.Sprintf("Server at maximum capacity (%d connections). Please try again later.", connManager.GetMaxConnections()),
			}
			rejectionBytes, _ := json.Marshal(rejectionMsg)
			conn.Write(append(rejectionBytes, '\n'))
			conn.Close()
			continue
		}

		fmt.Printf("New connection from %s (active: %d/%d)\n",
			conn.RemoteAddr(), connManager.GetActiveConnections(), connManager.GetMaxConnections())

		go handleConnectionWithManager(conn, spaceManager, authManager, connManager)
	}
}

// handleSignals handles runtime connection limit updates via signals
func handleSignals(cm *ConnectionManager) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1, syscall.SIGUSR2)

	for sig := range sigChan {
		currentLimit := cm.GetMaxConnections()
		var newLimit int32

		switch sig {
		case syscall.SIGUSR1:
			// Increase limit by 100
			newLimit = currentLimit + 100
			fmt.Printf("Received SIGUSR1: Increasing connection limit from %d to %d\n", currentLimit, newLimit)
		case syscall.SIGUSR2:
			// Decrease limit by 100, but not below current active connections
			active := cm.GetActiveConnections()
			newLimit = currentLimit - 100
			if newLimit < active {
				newLimit = active
				fmt.Printf("Received SIGUSR2: Cannot decrease below active connections (%d), keeping limit at %d\n", active, currentLimit)
				continue
			}
			fmt.Printf("Received SIGUSR2: Decreasing connection limit from %d to %d\n", currentLimit, newLimit)
		}

		if err := cm.UpdateLimit(newLimit); err != nil {
			fmt.Printf("Failed to update connection limit: %v\n", err)
		}
	}
}

// monitorConnections periodically logs connection statistics
func monitorConnections(cm *ConnectionManager) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := cm.GetConnectionStats()
		active := stats["active_connections"].(int32)
		max := stats["max_connections"].(int32)
		usage := stats["usage_percentage"].(float64)

		if usage > 80 {
			fmt.Printf("WARNING: High connection usage: %d/%d (%.1f%%)\n", active, max, usage)
		} else {
			fmt.Printf("Connection status: %d/%d (%.1f%%)\n", active, max, usage)
		}
	}
}

func formatStartupDuration(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

func handleConnectionWithManager(conn net.Conn, spaceManager *spaces.SpaceManager, authManager *auth.AuthManager, connManager *ConnectionManager) {
	defer func() {
		conn.Close()
		connManager.Release(conn)
		fmt.Printf("Connection closed from %s (active: %d/%d)\n",
			conn.RemoteAddr(), connManager.GetActiveConnections(), connManager.GetMaxConnections())
	}()

	handleConnection(conn, spaceManager, authManager, connManager)
}

func handleConnection(conn net.Conn, spaceManager *spaces.SpaceManager, authManager *auth.AuthManager, connManager *ConnectionManager) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Expect login first
	connManager.MarkConnectionBusy(conn)
	line, err := reader.ReadBytes('\n')
	connManager.MarkConnectionIdle(conn)
	if err != nil {
		fmt.Fprintf(conn, `{"status":"ERROR","message":"authentication failed"}`+"\n")
		return
	}

	var login models.LoginRequest
	connManager.MarkConnectionBusy(conn)
	if err := json.Unmarshal(line, &login); err != nil {
		connManager.MarkConnectionIdle(conn)
		fmt.Fprintf(conn, `{"status":"ERROR","message":"invalid login format"}`+"\n")
		return
	}
	connManager.MarkConnectionIdle(conn)

	connManager.MarkConnectionBusy(conn)
	user, err := authManager.Authenticate(login.Username, login.Password)
	connManager.MarkConnectionIdle(conn)
	if err != nil {
		fmt.Fprintf(conn, `{"status":"ERROR","message":"%s"}`+"\n", err.Error())
		return
	}

	resp := map[string]interface{}{
		"status": "OK",
		"user":   user,
	}
	connManager.MarkConnectionBusy(conn)
	respBytes, _ := json.Marshal(resp)
	fmt.Fprintf(conn, string(respBytes)+"\n")
	connManager.MarkConnectionIdle(conn)

	// Auth success
	qe := queryengine.NewQueryEngine(spaceManager, authManager)

	for {
		connManager.MarkConnectionBusy(conn)
		req, err := reader.ReadBytes('\n')
		connManager.MarkConnectionIdle(conn)
		if err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"connection closed"}`+"\n")
			return
		}

		var query models.Query
		connManager.MarkConnectionBusy(conn)
		if err := json.Unmarshal(req, &query); err != nil {
			connManager.MarkConnectionIdle(conn)
			fmt.Fprintf(conn, `{"status":"ERROR","message":"invalid query"}`+"\n")
			continue
		}
		connManager.MarkConnectionIdle(conn)
		query.User = login.Username

		// Enforce role-based access
		switch strings.ToUpper(query.Type) {
		case "CREATE_SPACE", "LIST_SPACES":
			if user.Role != auth.RoleAdmin {
				fmt.Fprintf(conn, `{"status":"ERROR","message":"admin access required"}`+"\n")
				continue
			}
		case "PUT", "DELETE":
			if !authManager.HasRole(user, query.Space, auth.RoleWrite) {
				fmt.Fprintf(conn, `{"status":"ERROR","message":"write permission denied"}`+"\n")
				continue
			}
		case "GET":
			if !(authManager.HasRole(user, query.Space, auth.RoleRead) ||
				authManager.HasRole(user, query.Space, auth.RoleWrite)) {
				fmt.Fprintf(conn, `{"status":"ERROR","message":"read permission denied"}`+"\n")
				continue
			}
		// Vector engine access checks
		case "INSERT_VECTOR", "DELETE_VECTOR":
			if !(user.Role == auth.RoleAdmin || authManager.HasRole(user, query.Space, auth.RoleWrite)) {
				fmt.Fprintf(conn, `{"status":"ERROR","message":"write permission denied"}`+"\n")
				continue
			}
		case "SEARCH_TOPK", "GET_VECTOR", "RANGE_SEARCH":
			if !(user.Role == auth.RoleAdmin || authManager.HasRole(user, query.Space, auth.RoleRead) || authManager.HasRole(user, query.Space, auth.RoleWrite)) {
				fmt.Fprintf(conn, `{"status":"ERROR","message":"read permission denied"}`+"\n")
				continue
			}
		}

		// Execute query
		connManager.MarkConnectionBusy(conn)
		result, err := qe.Execute(query)
		connManager.MarkConnectionIdle(conn)
		if err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"%s"}`+"\n", err.Error())
			continue
		}

		// Properly marshal all responses to handle all data types correctly
		// This ensures vector search results (JSON arrays/objects) and any other
		// complex data is properly escaped and remains valid JSON
		response := map[string]interface{}{
			"status": "OK",
		}

		if strings.ToUpper(query.Type) == "GET" {
			response["value"] = result
		} else {
			response["message"] = result
		}

		responseBytes, err := json.Marshal(response)
		if err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"failed to marshal response"}`+"\n")
			continue
		}
		connManager.MarkConnectionBusy(conn)
		fmt.Fprintf(conn, "%s\n", string(responseBytes))
		connManager.MarkConnectionIdle(conn)
	}
}
