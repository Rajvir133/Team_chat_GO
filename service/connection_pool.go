package service

import (
	"go_files/config"
	"net"
	"sync"
)

// Connection represents a client connection with metadata
type Connection struct {
	Conn     net.Conn
	UserName string
}

// ConnectionPool manages all active TCP connections
type ConnectionPool struct {
	connections map[string]*Connection // key: IP:Port address
	mu          sync.RWMutex
}

// Global connection pool instance
var Pool *ConnectionPool

// Initialize connection pool
func init() {
	Pool = &ConnectionPool{
		connections: make(map[string]*Connection),
	}
	config.DebugLog("Connection pool initialized")
}

// AddConnection adds a new connection to the pool
func (cp *ConnectionPool) AddConnection(address string, conn net.Conn, userName string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	cp.connections[address] = &Connection{
		Conn:     conn,
		UserName: userName,
	}

	config.DebugLog("Connection added to pool - Address: %s, User: %s", address, userName)
}

// RemoveConnection removes a connection from the pool
func (cp *ConnectionPool) RemoveConnection(address string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	delete(cp.connections, address)

	config.DebugLog("Connection removed from pool - Address: %s", address)
	config.InfoLog("R_Active connections: %d", len(cp.connections))
}

// GetConnection retrieves a connection by full address (IP:Port)
func (cp *ConnectionPool) GetConnection(address string) (*Connection, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	conn, exists := cp.connections[address]
	return conn, exists
}

// ListAllConnections returns all active connections
func (cp *ConnectionPool) ListAllConnections() map[string]*Connection {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	// Create a copy to avoid external modifications
	connectionsCopy := make(map[string]*Connection, len(cp.connections))
	for address, conn := range cp.connections {
		connectionsCopy[address] = conn
	}

	return connectionsCopy
}