package service

import (
	"fmt"
	"go_files/config"
	"net"
)

func StartTCPServer() {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.TCPPort))
	if err != nil {
		config.ErrorLog("Failed to start TCP server: %v", err)
		return
	}
	defer listener.Close()

	config.InfoLog("TCP server starting on port %d", config.TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			config.ErrorLog("Error accepting connection: %v", err)
			continue 
		}

		go handleTCPConnection(conn)
	}
}

func handleTCPConnection(conn net.Conn) {

	config.InfoLog("new request %s", conn.RemoteAddr().String())

	address := conn.RemoteAddr().String()
	userName := "Guest"
	
	if existingConn, exists := Pool.GetConnection(address); exists {
		if existingConn.UserName == userName {
			config.DebugLog("Connection from %s already exists with same values, skipping", address)
			conn.Close()
			return
		}
		config.DebugLog("Connection from %s exists but values changed, replacing", address)
		existingConn.Conn.Close()
	}

	Pool.AddConnection(address, conn, userName)
	config.DebugLog("New TCP connection stored from %s", address)

	defer func() {
		Pool.RemoveConnection(address)
		conn.Close()
		config.DebugLog("Connection closed: %s", address)
	}()

	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			config.DebugLog("Connection error from %s: %v", address, err)
			return
		}
	}
}