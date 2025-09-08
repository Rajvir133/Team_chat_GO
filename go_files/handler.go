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
	if r.Method != http.MethodPost {
		http.Error(w, `{"success":false,"error":{"code":"METHOD_NOT_ALLOWED","message":"use POST"}}`, http.StatusMethodNotAllowed)
		return
	}

	msg, err := create_payload(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"BAD_REQUEST","message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}

	start := time.Now()
	if err := transfer.Send_TCP(msg); err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"FORWARDING_FAILED","message":%q}}`, err.Error()), http.StatusBadGateway)
		return
	}
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"sender":       msg.Sender,
			"receiver":     msg.Receiver,
			"message_type": msg.MessageType,
			"time_taken_ms": elapsed.Milliseconds(),
			"time_taken_s":  elapsed.Seconds(),
		},
	})
}


// ======================
// /scan endpoint
// ======================
func ScanHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	devices := []string{}
	for i := 1; i <= 255; i++ {
		ip := fmt.Sprintf("%s%d", config.IPBase, i)
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, config.TCPPort), 100*time.Millisecond); err == nil {
			_ = conn.Close()
			devices = append(devices, ip)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"devices": devices,
		"duration_ms": time.Since(start).Milliseconds(),
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
