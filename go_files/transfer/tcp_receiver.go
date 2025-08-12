package transfer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	"go_files/config"
)

// StartTCPServer listens for TCP connections from other devices
func StartTCPServer(tcpPort int) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
	if err != nil {
		log.Fatalf("TCP listen error: %v", err)
	}
	log.Printf("🔌 TCP server listening on port %d...\n", tcpPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[!] TCP accept error: %v\n", err)
			continue
		}
		go handleTCPConnection(conn)
	}
}

// handleTCPConnection processes incoming TCP messages or file metadata
func handleTCPConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	metaLine, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("[!] Failed to read incoming TCP data: %v\n", err)
		return
	}

	// First, try parsing as Message (non-file)
	var msg config.Message
	if err := json.Unmarshal([]byte(metaLine), &msg); err == nil && msg.MessageType == "text" {
		log.Printf("[📨] Text message from %s: %s\n", msg.Sender, msg.Message)
		log.Printf("[🔄] Forwarding text message to FastAPI...")
		forwardToFastAPI(msg)
		return
	}

	// Otherwise, treat it as FileMetadata
	var metadata config.FileMetadata
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		log.Printf("[!] Invalid metadata format: %v\n", err)
		return
	}

	log.Printf("[📦] Incoming file: %s (%d bytes, %d chunks) from %s\n",
		metadata.Name, metadata.Size, metadata.Chunks, metadata.Sender)

	// Start UDP receiver
	log.Printf("[🔌] Starting UDP receiver for file: %s", metadata.Name)
	if err := startUDPReceiver(conn, metadata); err != nil {
		log.Printf("[!] UDP receive error: %v\n", err)
		return
	}

	// Wait for final ACK ("stop\n") before forwarding to FastAPI
	log.Printf("[⏳] Waiting for final completion ACK...")
	finalAck, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("[!] Failed to read final ACK: %v\n", err)
		return
	}

	if finalAck != "stop\n" {
		log.Printf("[!] Transfer for %s did not complete successfully: %s", metadata.Name, finalAck)
		return
	}

	log.Printf("[✅] File transfer completed successfully for: %s", metadata.Name)

	// After success, forward reconstructed file to FastAPI
	log.Printf("[🔄] Forwarding completed file to FastAPI...")
	fileMsg := config.Message{
		Sender:      metadata.Sender,
		Receiver:    metadata.Receiver,
		MessageType: metadata.Type,
		Payload: []config.FilePayload{
			{
				Name: metadata.Name,
				Type: metadata.Type,
				Data: config.EncodeBase64FromFile("received_media/" + metadata.Name),
			},
		},
	}
	forwardToFastAPI(fileMsg)
}

// forwardToFastAPI sends the message to the local /receive endpoint
func forwardToFastAPI(msg config.Message) {
	body, _ := json.Marshal(msg)
	url := fmt.Sprintf("http://127.0.0.1:%d/receive", config.HTTPPort) // local handler

	log.Printf("[🌐] Forwarding %s message to FastAPI at %s", msg.MessageType, url)

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[!] Failed to forward to /receive: %v\n", err)
		return
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)
	log.Printf("[✅] Successfully forwarded %s message to FastAPI (Status: %d)",
		msg.MessageType, resp.StatusCode)
}
