package main

import (
	"encoding/json"
	"fmt"
	"net"
	"io"
	"net/http"
	"time"

	"go_files/config"
	"go_files/transfer"
)

func SendHandler(w http.ResponseWriter, r *http.Request) {
    start := time.Now()
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    msg, err := create_payload(r)
    if err != nil {
        http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
        return
    }

    if err := transfer.Send_TCP(msg); err != nil {
        http.Error(w, fmt.Sprintf("send failed: %v", err), http.StatusBadGateway)
        return
    }

    elapsed := time.Since(start)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "success": true,
        "time_taken_ms":  elapsed.Milliseconds(),
        "time_taken_s":   elapsed.Seconds(),
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




func create_payload(r *http.Request) (config.Message, error) {
    if err := r.ParseMultipartForm(128 << 20); err != nil {
        return config.Message{}, fmt.Errorf("parse multipart: %w", err)
    }

    msg := config.Message{
        Sender:      r.FormValue("sender"),
        Receiver:    r.FormValue("receiver"),
        MessageType: r.FormValue("message_type"),
        Message:     r.FormValue("message"),
    }

    if msg.Sender == "" || msg.Receiver == "" || msg.MessageType == "" {
        return config.Message{}, fmt.Errorf("required fields missing")
    }

    msg.Payload = make([]config.FilePayload, 0) 

	if msg.MessageType != "text" && r.MultipartForm != nil {
		if fhs, ok := r.MultipartForm.File["files"]; ok {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					return config.Message{}, fmt.Errorf("open %s: %w", fh.Filename, err)
				}
				data, err := io.ReadAll(f)
				_ = f.Close()
				if err != nil {
					return config.Message{}, fmt.Errorf("read %s: %w", fh.Filename, err)
				}

				if len(data) == 0 {
					continue
				}

				ct := fh.Header.Get("Content-Type")
				if ct == "" {
					ct = "application/octet-stream"
				}

				msg.Payload = append(msg.Payload, config.FilePayload{
					Name: fh.Filename, Type: ct, Data: data,
				})
			}
		}
	}

	// if no usable files, treat it as a text message
	if len(msg.Payload) == 0 {
		msg.MessageType = "text"
	}

	return msg, nil
}
