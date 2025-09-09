package main

import (
	"encoding/json"
	"fmt"
	"net"
	"io"
	"strings"
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

	var conn net.Conn

	if v, ok := connectionPool.Load(msg.Receiver); ok {
		conn = v.(net.Conn)
	} else {

		c, err := dialTo(msg.Receiver)
		if err != nil {
			http.Error(w, `{"success":false,"error":{"code":"CONNECTION_NOT_FOUND","message":"receiver offline or unreachable"}}`, http.StatusBadGateway)
			return
		}

		if old, loaded := connectionPool.LoadAndDelete(msg.Receiver); loaded {
			if oc, ok := old.(net.Conn); ok && oc != c {
				_ = oc.Close()
			}
		}
		connectionPool.Store(msg.Receiver, c)
		conn = c
	}

	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := transfer.Send_TCP(msg, conn); err != nil {

		if old, ok := connectionPool.LoadAndDelete(msg.Receiver); ok {
			if oc, ok2 := old.(net.Conn); ok2 {
				_ = oc.Close()
			}
		}

		c2, err2 := dialTo(msg.Receiver)
		if err2 != nil {
			http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"FORWARDING_FAILED","message":"%v / retry dial: %v"}}`, err, err2), http.StatusBadGateway)
			return
		}
		connectionPool.Store(msg.Receiver, c2)
		_ = c2.SetWriteDeadline(time.Now().Add(10 * time.Second))

		if err3 := transfer.Send_TCP(msg, c2); err3 != nil {
			http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"FORWARDING_FAILED","message":"retry send: %v"}}`, err3), http.StatusBadGateway)
			return
		}
	}

	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"sender":        msg.Sender,
			"receiver":      msg.Receiver,
			"message_type":  msg.MessageType,
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
			devices = append(devices, ip)
			go establishConnection(conn)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"devices": devices,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}






func establishConnection(conn net.Conn) {
	ip := extractIP(conn.RemoteAddr().String())
	fmt.Printf("[logs] persistent connection established: %s\n", ip)

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		_ = tcp.SetNoDelay(true)
	}

	// Keep the freshest conn for this IP
	if old, loaded := connectionPool.LoadAndDelete(ip); loaded {
		if oc, ok := old.(net.Conn); ok && oc != conn {
			_ = oc.Close()
		}
	}
	connectionPool.Store(ip, conn)
}

func extractIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

func dialTo(ip string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, config.TCPPort), 2*time.Second)
	if err != nil {
		return nil, err
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		_ = tcp.SetNoDelay(true)
	}
	return c, nil
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

    if msg.Sender == "" || msg.Receiver == "" {
        return config.Message{}, fmt.Errorf("required fields missing")
    }

	msg.Payload = make([]config.FilePayload, 0)
	if r.MultipartForm != nil {
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

	// Auto-set message_type
	if len(msg.Payload) > 0 {
		msg.MessageType = "file"
	} else {
		msg.MessageType = "text"
	}

	return msg, nil
}
