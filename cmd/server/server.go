package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
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
	semaphore         chan struct{}
	connections       sync.Map
	mu                sync.RWMutex
	// Dynamic limit management
	limitUpdateChan chan int32
	shutdownChan    chan struct{}
}

// NewConnectionManager creates a new connection manager with the specified limit
func NewConnectionManager(maxConnections int32) *ConnectionManager {
	cm := &ConnectionManager{
		maxConnections:  maxConnections,
		semaphore:       make(chan struct{}, maxConnections),
		limitUpdateChan: make(chan int32, 10), // Buffer for limit updates
		shutdownChan:    make(chan struct{}),
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
	if err := SaveConnectionLimit(newLimit); err != nil {
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
		cm.connections.Store(conn.RemoteAddr().String(), conn)
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
	max := cm.maxConnections
	usage := float64(active) / float64(max) * 100

	return map[string]interface{}{
		"active_connections": active,
		"max_connections":    max,
		"usage_percentage":   usage,
		"available_slots":    max - active,
	}
}

// CloseAllConnections forcefully closes all active connections
func (cm *ConnectionManager) CloseAllConnections() {
	cm.connections.Range(func(key, value interface{}) bool {
		if conn, ok := value.(net.Conn); ok {
			conn.Close()
		}
		return true
	})
}

// Shutdown gracefully shuts down the connection manager
func (cm *ConnectionManager) Shutdown() {
	close(cm.shutdownChan)
}

func StartServer(port string, authFilePath string, maxConnections int32, dataFolderPath string) {
	spaceManager := spaces.NewSpaceManager(dataFolderPath)
	defer spaceManager.CloseAll()

	authManager, err := auth.NewAuthManager(authFilePath)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize auth: %v", err))
	}

	// Load persistent connection limit if available
	persistentLimit := GetPersistentLimit(maxConnections)
	actualLimit := maxConnections
	if persistentLimit != maxConnections {
		fmt.Printf("Using persisted connection limit: %d (instead of %d)\n", persistentLimit, maxConnections)
		actualLimit = persistentLimit
	}

	// Create connection manager
	connManager := NewConnectionManager(actualLimit)
	defer connManager.Shutdown()

	// Start connection monitoring goroutine
	go monitorConnections(connManager)

	// Start signal handler for runtime limit updates
	go handleSignals(connManager)

	// Start management server on port + 1000
	managementPort := fmt.Sprintf("%d", getPortAsInt(port)+1000)
	managementServer := NewManagementServer(connManager, managementPort)
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
	fmt.Printf("HTTP management: GET/PUT http://localhost:%s/limit\n", managementPort)

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

// getPortAsInt converts port string to int
func getPortAsInt(port string) int {
	if portInt, err := strconv.Atoi(port); err == nil {
		return portInt
	}
	return 9090 // default fallback
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

func handleConnectionWithManager(conn net.Conn, spaceManager *spaces.SpaceManager, authManager *auth.AuthManager, connManager *ConnectionManager) {
	defer func() {
		conn.Close()
		connManager.Release(conn)
		fmt.Printf("Connection closed from %s (active: %d/%d)\n",
			conn.RemoteAddr(), connManager.GetActiveConnections(), connManager.GetMaxConnections())
	}()

	handleConnection(conn, spaceManager, authManager)
}

func handleConnection(conn net.Conn, spaceManager *spaces.SpaceManager, authManager *auth.AuthManager) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Expect login first
	line, err := reader.ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(conn, `{"status":"ERROR","message":"authentication failed"}`+"\n")
		return
	}

	var login models.LoginRequest
	if err := json.Unmarshal(line, &login); err != nil {
		fmt.Fprintf(conn, `{"status":"ERROR","message":"invalid login format"}`+"\n")
		return
	}

	user, err := authManager.Authenticate(login.Username, login.Password)
	if err != nil {
		fmt.Fprintf(conn, `{"status":"ERROR","message":"%s"}`+"\n", err.Error())
		return
	}

	resp := map[string]interface{}{
		"status": "OK",
		"user":   user,
	}
	respBytes, _ := json.Marshal(resp)
	fmt.Fprintf(conn, string(respBytes)+"\n")

	// Auth success
	qe := queryengine.NewQueryEngine(spaceManager, authManager)

	for {
		req, err := reader.ReadBytes('\n')
		if err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"connection closed"}`+"\n")
			return
		}

		var query models.Query
		if err := json.Unmarshal(req, &query); err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"invalid query"}`+"\n")
			continue
		}
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
		case "INSERT_VECTOR":
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
		result, err := qe.Execute(query)
		if err != nil {
			fmt.Fprintf(conn, `{"status":"ERROR","message":"%s"}`+"\n", err.Error())
			continue
		}

		if strings.ToUpper(query.Type) == "GET" {
			fmt.Fprintf(conn, `{"status":"OK","value":"%s"}`+"\n", result)
		} else {
			fmt.Fprintf(conn, `{"status":"OK","message":"%s"}`+"\n", result)
		}
	}
}
