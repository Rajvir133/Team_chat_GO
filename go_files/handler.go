package main

import (
	"encoding/json"
	"bufio"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
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

	// If not text, ensure we actually received a file before doing any socket work.
	if msg.MessageType != "TEXT" && len(msg.Payload) == 0 {
		http.Error(w, `{"success":false,"error":{"code":"BAD_REQUEST","message":"no file uploaded: use multipart field 'files'/'file'/'video'/'image'/'media'"}}`, http.StatusBadRequest)
		return
	}

	// Resolve receiver hostname/IP for transport (keep msg.Receiver as-is for metadata)
	dialIP, err := resolveDialIP(msg.Receiver)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"UNKNOWN_RECEIVER","message":"%s"}}`, err.Error()), http.StatusNotFound)
		return
	}
	ip := dialIP // use this for pooling/dialing/locking

	start := time.Now()
	var conn net.Conn

	// 1) Try any existing connection (outgoing or incoming)
	if c, ok := getAnyConn(ip); ok {
		conn = c
		fmt.Println("[logs] Reusing existing conn (any dir)")
	} else {
		// 2) Serialize dials per IP
		lock := getDialLock(ip)
		lock.Lock()
		{
			// Double-check after acquiring lock
			if c2, ok2 := getAnyConn(ip); ok2 {
				conn = c2
				fmt.Println("[logs] Reusing existing conn after lock")
			} else {
				fmt.Println("[logs] No conn present, dialing...")
				var dconn net.Conn
				var derr error
				for attempt := 1; attempt <= config.Max_connection_retry; attempt++ {
					dconn, derr = dialTo(ip)
					if derr == nil {
						storeOutgoingConn(ip, dconn)
						conn = dconn
						fmt.Printf("[logs] Dial succeeded at attempt %d\n", attempt)
						break
					}
				}
				if conn == nil {
					fmt.Println("[logs] Device is offline (dial failed)")
					http.Error(w, `{"success":false,"error":{"code":"CONNECTION_NOT_FOUND","message":"device is offline"}}`, http.StatusBadGateway)
					lock.Unlock()
					return
				}
			}
		}
		lock.Unlock()
	}

	// 3) Send (with one retry on send failure), serialized per resolved IP
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	sendLock := getSendLock(ip) // lock by IP (not hostname)
	sendLock.Lock()
	sendErr := transfer.Send_TCP(msg, conn)
	sendLock.Unlock()

	if sendErr != nil {
		// purge stale mapping if it matches
		if cur, ok := getAnyConn(ip); ok && cur == conn {
			// best-effort delete both directions
			if v, ok := connectionPool.Load(ip); ok && v == conn {
				connectionPool.Delete(ip)
				_ = conn.Close()
			}
			if v, ok := connectionPool.Load(ip + "|in"); ok && v == conn {
				connectionPool.Delete(ip + "|in")
				_ = conn.Close()
			}
		}

		// Serialize retry dial
		lock := getDialLock(ip)
		lock.Lock()
		{
			// Reuse if someone else restored a conn while we waited
			if c2, ok2 := getAnyConn(ip); ok2 {
				conn = c2
			} else {
				// single retry dial
				c2, err2 := dialTo(ip)
				if err2 != nil {
					http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"FORWARDING_FAILED","message":"%v / retry dial: %v"}}`, sendErr, err2), http.StatusBadGateway)
					lock.Unlock()
					return
				}
				storeOutgoingConn(ip, c2)
				conn = c2
			}
		}
		lock.Unlock()

		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		sendLock.Lock()
		err3 := transfer.Send_TCP(msg, conn)
		sendLock.Unlock()
		if err3 != nil {
			http.Error(w, fmt.Sprintf(`{"success":false,"error":{"code":"FORWARDING_FAILED","message":"retry send: %v"}}`, err3), http.StatusBadGateway)
			return
		}
	}

	elapsed := time.Since(start)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"sender":        msg.Sender,   // hostname preserved
			"receiver":      msg.Receiver, // hostname preserved
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"devices":     devices,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}








func establishConnection(conn net.Conn) {
    ip := extractIP(conn.RemoteAddr().String())
    fmt.Printf("[logs] persistent connection established: %s\n", ip)
    config.Send_identity(conn) // you already do this

    if tcp, ok := conn.(*net.TCPConn); ok {
        _ = tcp.SetKeepAlive(true)
        _ = tcp.SetKeepAlivePeriod(30 * time.Second)
        _ = tcp.SetNoDelay(true)
    }

    // NEW: try to read peer's identity echo (one line) and record it
    go func() {
        r := bufio.NewReader(conn)
        _ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
        line, err := r.ReadString('\n')
        _ = conn.SetReadDeadline(time.Time{})
        if err == nil {
            s := strings.TrimSpace(line)
            if i := strings.Index(s, "|"); i > 0 {
                tag := strings.TrimSpace(s[:i])
                host := strings.TrimSpace(s[i+1:])
                if tag == "BMS" || tag == "CMK" {
                    if transfer.OnIdentity != nil {
                        transfer.OnIdentity(ip, host)
                    }
                    fmt.Printf("[ receive ]  %s|%s from %s <-----------\n",tag, host, ip)
                }
            }
        }
        // Ignore errors/timeouts: not fatal
    }()

    // Store as OUTGOING
    storeOutgoingConn(ip, conn)
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
	config.Send_identity(c)
	return c, nil
}

// ======================
// Payload parsing
// ======================

func create_payload(r *http.Request) (config.Message, error) {
	// allow large uploads; bigger files spill to disk temp
	if err := r.ParseMultipartForm(512 << 20); err != nil {
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
		// Preferred keys first
		preferred := []string{"files", "file", "video", "image", "media"}
		added := 0
		for _, key := range preferred {
			added += appendFilesFromField(key, r.MultipartForm, &msg)
		}

		// Fallback: sweep all fields if nothing matched
		if added == 0 {
			for key, fhs := range r.MultipartForm.File {
				n := 0
				for _, fh := range fhs {
					fp, err := readFilePayload(fh)
					if err != nil {
						log.Printf("[send] skip %s (%s): %v", key, fh.Filename, err)
						continue
					}
					if len(fp.Data) == 0 {
						continue
					}
					msg.Payload = append(msg.Payload, fp)
					n++
				}
				if n > 0 {
					log.Printf("[send] accepted files from field %q (fallback), count=%d", key, n)
				}
			}
		}
	}

	return msg, nil
}

func appendFilesFromField(key string, mf *multipart.Form, msg *config.Message) int {
	fhs, ok := mf.File[key]
	if !ok {
		return 0
	}
	n := 0
	for _, fh := range fhs {
		fp, err := readFilePayload(fh)
		if err != nil {
			log.Printf("[send] skip %s (%s): %v", key, fh.Filename, err)
			continue
		}
		if len(fp.Data) == 0 {
			continue
		}
		msg.Payload = append(msg.Payload, fp)
		n++
	}
	if n > 0 {
		log.Printf("[send] accepted files from field %q, count=%d", key, n)
	}
	return n
}

func readFilePayload(fh *multipart.FileHeader) (config.FilePayload, error) {
	f, err := fh.Open()
	if err != nil {
		return config.FilePayload{}, fmt.Errorf("open %s: %w", fh.Filename, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return config.FilePayload{}, fmt.Errorf("read %s: %w", fh.Filename, err)
	}
	ct := fh.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	return config.FilePayload{
		Name: fh.Filename,
		Type: ct,
		Data: data,
	}, nil
}

// ======================
// Hostname/IP resolver
// ======================

func resolveDialIP(s string) (string, error) {
	// 1) Try our live registry first (identity-fed)
	if ip, ok := resolveFromRegistry(s); ok {
		return ip, nil
	}
	// 2) If s is an IP, accept it
	if ip := net.ParseIP(s); ip != nil {
		return ip.String(), nil
	}
	// 3) Fallback to OS resolver (DNS/mDNS if available)
	addrs, err := net.LookupIP(s)
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("unable to resolve %q", s)
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	// if no IPv4, return the first
	return addrs[0].String(), nil
}
