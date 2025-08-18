package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"go_files/config"
	"go_files/transfer"
)

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

	if err := transfer.SendFileTCP(msg); err != nil {
		http.Error(w, fmt.Sprintf("Send failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"sender":       msg.Sender,
			"receiver":     msg.Receiver,
		},
	})
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