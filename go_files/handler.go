package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"go_files/config"
	"go_files/transfer"
)

// ======================
// /send endpoint
// ======================
func SendHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg config.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("[HTTP] /send request from %s to %s (%s)\n", msg.Sender, msg.Receiver, msg.MessageType)

	if err := transfer.SendMessage(msg, config.TCPPort); err != nil {
		http.Error(w, fmt.Sprintf("Send failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Message sent successfully"))
}

// ======================
// /scan endpoint
// ======================
func ScanHandler(w http.ResponseWriter, r *http.Request) {
	devices := []string{}

	for i := 1; i <= 255; i++ {
		ip := fmt.Sprintf("%s%d", config.IPBase, i)
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, config.TCPPort), 100*time.Millisecond)
		if err == nil {
			devices = append(devices, ip)
			conn.Close()
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": devices,
	})
}

// ======================
// /receive endpoint
// ======================
func ReceiveHandler(w http.ResponseWriter, r *http.Request) {
	var msg config.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid message: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("[HTTP] Forwarding message from %s to FastAPI...\n", msg.Sender)

	fastapiURL := fmt.Sprintf("http://%s:%d/receive", config.FastAPIHost, config.FastAPIPort)
	body, _ := json.Marshal(msg)

	resp, err := http.Post(fastapiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to forward to FastAPI: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}
