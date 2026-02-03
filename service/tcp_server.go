package service

import (
	"fmt"
	"go_files/config"
	"log"
	"net"
)


func StartTCPServer() {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.TCPPort))
	if err != nil {
		config.ErrorLog("Failed to start TCP server: %v", err)
	}
	defer listener.Close()

	config.InfoLog("TCP_Server on port %d", config.TCPPort)

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
	defer conn.Close()
	log.Printf("New TCP connection from %s", conn.RemoteAddr().String())

	// Handle TCP connection here
	// For now, just keep connection open
}