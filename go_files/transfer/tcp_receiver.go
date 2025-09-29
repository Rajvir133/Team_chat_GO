package transfer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"io"
	"net/http"

	"strings"
	"time"
	"net/textproto"

	"go_files/config"
)

var onConnClosed func(ip string, conn net.Conn)
var onConnReady  func(ip string, conn net.Conn)

func SetOnConnClosed(fn func(ip string, conn net.Conn)) { onConnClosed = fn }
func SetOnConnReady(fn func(ip string, conn net.Conn))  { onConnReady  = fn }

func StartTCPServer(port int) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil { panic(err) }
	fmt.Printf("[TCP] server listening on port %d...\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("[Error] TCP accept error:", err)
			continue
		}
		ip := extractIP(conn.RemoteAddr().String())
		fmt.Printf("[TCP] new connection request from %s\n", ip)

		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(true)
			_ = tcp.SetKeepAlivePeriod(30 * time.Second)
			_ = tcp.SetNoDelay(true)
		}
		go handleTCPConnection(conn)
	}
}

func handleTCPConnection(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		_ = tcp.SetNoDelay(true)
	}

	// Initialize peer IP once; avoid shadowing later
	ip := extractIP(conn.RemoteAddr().String())

	defer func() {
		if onConnClosed != nil { onConnClosed(ip, conn) }
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)

	var raw_msg string
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	firstLine, err := reader.ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})

	if err == nil {
		line := strings.TrimSpace(firstLine)
		// Early identity? format: tag|host
		if i := strings.Index(line, "|"); i > 0 {
			tag  := strings.TrimSpace(line[:i])
			host := strings.TrimSpace(line[i+1:])
			if tag == "BMS" || tag == "CMK" {
				ip = extractIP(conn.RemoteAddr().String())
				fmt.Printf("[ receive ] %s|%s <---------- %s\n", host, tag, ip)
				if OnIdentity != nil { OnIdentity(ip, host,tag) }
				config.Send_identity(conn)
			} else {
				raw_msg = firstLine
			}
		} else {
			raw_msg = firstLine
		}
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		// no ID arrived (legacy peer) — proceed
	} else if err != nil {
		fmt.Println("[TCP] read error during identification:", err)
		return
	}

	if onConnReady != nil {
		ip = extractIP(conn.RemoteAddr().String())
		onConnReady(ip, conn)
	}

	for {
		// 1) Read one metadata line (text/file header)
		var metaLine string
		if raw_msg != "" {
			metaLine = raw_msg
			raw_msg = ""
		} else {
			var err error
			metaLine, err = reader.ReadString('\n')
			if err != nil {
				fmt.Println("[logs] closed the connection", extractIP(conn.RemoteAddr().String()), err)
				return
			}
		}

		fmt.Println(strings.Repeat("-", 50))
		fmt.Println(strings.Repeat(" ", 50))
		fmt.Printf("raw data  %s\n", metaLine)
		fmt.Println(strings.Repeat("-", 50))

		line := strings.TrimSpace(metaLine)

		// Heartbeats
		if line == "KEEP_ALIVE" {
			fmt.Printf("[hb] keep_alive <-------  %s\n", ip)
			_, _ = conn.Write([]byte("ALIVE\n"))
			fmt.Printf("[hb] ALIVE ------->  %s\n", ip)
			continue
		}
		if line == "PING" {
			fmt.Printf("[hb] PING <-------  %s\n", ip)
			_, _ = conn.Write([]byte("PONG\n"))
			fmt.Printf("[hb] PONG -------->  %s\n", ip)
			continue
		}

		// Late identity?
		if i := strings.Index(line, "|"); i > 0 {
			tag := strings.TrimSpace(line[:i])
			if tag == "BMS" || tag == "CMK" {
				host := strings.TrimSpace(line[i+1:])
				ip = extractIP(conn.RemoteAddr().String())
				fmt.Printf("[id] peer identified (late) ip=%s host=%s tag=%s\n", ip, host, tag)
				if OnIdentity != nil { OnIdentity(ip, host,tag) }
				continue
			}
		}

		// 2) TEXT: sender|receiver|text|TEXT|Host
		if fields, ok := tryPipe(line); ok && len(fields) >= 5 && strings.EqualFold(fields[3], "text") {
			metadata := config.FileMetadata{
				Sender:   fields[0],
				Receiver: fields[1],
				Type:     "TEXT",
				Message:  fields[2],
				Host:     fields[4],
			}
			go notifyFastAPI(metadata, nil)
			continue
		}

		// 3) FILE (JSON metadata header)
		if strings.HasPrefix(line, "{") {
		type unifiedMeta struct {
			From      string `json:"from"`
			To        string `json:"to"`
			Type      string `json:"type"`
			Name      string `json:"name"`
			Ext       string `json:"ext"`
			Size      int    `json:"size"`
			Checksum  string `json:"checksum"`
			Transport string `json:"transport"`
			XferId    string `json:"xferId"`
			Marker    string `json:"marker"`
		}
		var m unifiedMeta
		if err := json.Unmarshal([]byte(line), &m); err == nil && (m.Transport == "" || strings.EqualFold(m.Transport, "UDP")) {
			metadata := config.FileMetadata{
				Sender: m.From, Receiver: m.To,
				Name: m.Name, Type: m.Type, Ext: m.Ext,
				Size: m.Size, Hash: strings.ToLower(m.Checksum),
				Chunks: config.CalculateChunks(m.Size),
				Host: m.Marker,
				XferId: strings.ToLower(m.XferId),
			}
			// Create ephemeral UDP listener for this ONE transfer
			udpAddr, err := net.ResolveUDPAddr("udp", ":0")
			if err != nil { fmt.Println("[Error] resolve UDP addr:", err); continue }
			udpConn, err := net.ListenUDP("udp", udpAddr)
			if err != nil { fmt.Println("[Error] listen UDP:", err); continue }
			_ = udpConn.SetReadBuffer(4 << 20)
			udpPort := udpConn.LocalAddr().(*net.UDPAddr).Port

			// Tell sender which UDP port to use (always include xferId)
			startLine := fmt.Sprintf("START_UDP:%d:%s\n", udpPort, metadata.XferId)
			if _, err := conn.Write([]byte(startLine)); err != nil {
				fmt.Println("[Error] TCP write START_UDP:", err)
				udpConn.Close()
				continue
			}

			// Receive chunks over UDP; ACKs go back on this same TCP socket
			fileDataChan := make(chan []byte, 1)
			done := make(chan struct{}, 1)
			go func() {
				combined := StartUDPReceiverConn(udpConn, metadata, conn, done)
				fileDataChan <- combined
			}()

			// Wait for receive to finish (or timeout)
			select {
			case combined := <-fileDataChan:
				if combined == nil && metadata.Size > 0 {
					fmt.Println("[logs] receive failed (nil data)")
					udpConn.Close()
					_, _ = conn.Write([]byte("error:receive_failed\n"))
					continue
				}
				udpConn.Close()
				go notifyFastAPI(metadata, combined)
				fmt.Println("[logs] file delivered; keeping TCP open")
			case <-time.After(120 * time.Second):
				fmt.Println("[logs] timeout waiting for file data")
				udpConn.Close()
				_, _ = conn.Write([]byte("error:timeout\n"))
			}
			continue
			}
		}

		// Unknown line — ignore and continue (don’t kill the socket)
		fmt.Printf("[Error] unrecognized line (neither identity, text, nor JSON file meta): %q\n", line)
		continue
	}
}

func notifyFastAPI(metadata config.FileMetadata, combinedFileData []byte) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	_ = w.WriteField("sender", metadata.Sender)
	_ = w.WriteField("receiver", metadata.Receiver)
	_ = w.WriteField("message_type", metadata.Type)
	_ = w.WriteField("message", metadata.Message)

	if len(combinedFileData) > 0 && metadata.Name != "" {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "files", metadata.Name))
		ct := metadata.Type
		if ct == "" { ct = "application/octet-stream" }
		hdr.Set("Content-Type", ct)
		fw, err := w.CreatePart(hdr)
		if err != nil { return fmt.Errorf("create multipart part: %w", err) }
		if _, err := fw.Write(combinedFileData); err != nil { return fmt.Errorf("write file bytes: %w", err) }
	}

	if err := w.Close(); err != nil { return fmt.Errorf("close multipart writer: %w", err) }

	url := fmt.Sprintf("http://%s:%d/go_message", config.FastAPIHost, config.FastAPIPort)

	req, err := http.NewRequest("POST", url, &body)
	if err != nil { return fmt.Errorf("new request: %w", err) }
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return fmt.Errorf("http post: %w", err) }
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("FastAPI returned status %d: %s", resp.StatusCode, string(b))
	}

	fmt.Printf("[✓] Notified FastAPI (multipart) about %s (%d bytes, type %s)\n",
		metadata.Name, len(combinedFileData), metadata.Type)
	return nil
}

func extractIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil { return host }
	if i := strings.LastIndex(addr, ":"); i != -1 { return addr[:i] }
	return addr
}

// ----- helpers (pipe parsing) -----

// tryPipe splits a pipe-escaped line into fields.
// Returns ok=false if there is no '|' at all (i.e., not a pipe message).
func tryPipe(s string) ([]string, bool) {
	if !strings.Contains(s, "|") {
		return nil, false
	}
	out, _ := pipeSplit(s)
	return out, true
}

func pipeSplit(s string) ([]string, error) {
	var parts []string
	var b strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			switch c {
			case '|', '\\':
				b.WriteByte(c)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			default:
				// unknown escape: keep backslash + char
				b.WriteByte('\\')
				b.WriteByte(c)
			}
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '|' {
			parts = append(parts, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if escaped {
		// dangling backslash at end -> literal
		b.WriteByte('\\')
	}
	parts = append(parts, b.String())
	return parts, nil
}
