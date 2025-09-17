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

func SetOnConnClosed(fn func(ip string, conn net.Conn)) {
	onConnClosed = fn
}
func SetOnConnReady(fn func(ip string, conn net.Conn))  { 
	onConnReady  = fn 
}


func StartTCPServer(port int) {
    listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
    if err != nil {
        panic(err)
    }
    fmt.Printf("[TCP] server listening on port %d...\n", port)

    for {
        conn, err := listener.Accept()
        if err != nil {
            fmt.Println("[Error] TCP accept error:", err)
            continue
        }

		remote := conn.RemoteAddr().String()
        ip := extractIP(remote)

        // 👇 NEW: log that a new connection request arrived
        fmt.Printf("[TCP] new connection request from %s\n", ip)


        // 🔐 Keep socket healthy across NATs and reduce ACK latency
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


	defer func() {
		ip := extractIP(conn.RemoteAddr().String())
		if onConnClosed != nil {
			onConnClosed(ip, conn)
		}
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)


	var pending string
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	firstLine, err := reader.ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})

	if err == nil {
		line := strings.TrimSpace(firstLine)
		if i := strings.Index(line, "|"); i > 0 {
			tag := strings.TrimSpace(line[:i])
			host  := strings.TrimSpace(line[i+1:])
			if tag == "BMS" || tag == "CMK" {
				ip := extractIP(conn.RemoteAddr().String())
				fmt.Printf("[ receive ] Received %s|%s from %s \n",host, tag,ip)
				
				config.Send_identity(conn)
			} else {
				// not an ID; treat it as the first normal frame later
				pending = firstLine
			}
		} else {
			// not an ID; treat it as the first normal frame later
			pending = firstLine
		}
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		// no ID arrived (old peer) — proceed without sending ours
	} else if err != nil {
		fmt.Println("[TCP] read error during identification:", err)
		return
	}
	
	if onConnReady != nil {
		ip := extractIP(conn.RemoteAddr().String())
		onConnReady(ip, conn)
	}

	for {
		// 1) Read one metadata line (either text message or file metadata)
		var metaLine string
		if pending != "" {
			metaLine = pending
			pending = ""
		} else {
			var err error
			metaLine, err = reader.ReadString('\n')
			if err != nil {
				fmt.Println("[logs] closed the connection",extractIP(conn.RemoteAddr().String()), err)
				return
			}
		}



		line := strings.TrimSpace(metaLine)

		if line == "PING" {
			_, _ = conn.Write([]byte("PONG\n"))
			fmt.Println("[hb] got PING, sent PONG")
			continue
		}
		if line == "PONG" {
			fmt.Println("[hb] got PONG (ignored)")
			continue
		}


		if i := strings.Index(line, "|"); i > 0 {
			host := strings.TrimSpace(line[:i])
			tag  := strings.TrimSpace(line[i+1:])
			if tag == "BMS" || tag == "CMK" {
				ip := extractIP(conn.RemoteAddr().String())
				fmt.Printf("[id] peer identified (late) ip=%s host=%s tag=%s\n", ip, host, tag)
				continue
			}
    	}



		// 2) Try TEXT path first
		var msg config.Message
		if err := json.Unmarshal([]byte(metaLine), &msg); err == nil && msg.MessageType == "text" {
			metadata := config.FileMetadata{
				Sender:   msg.Sender,
				Receiver: msg.Receiver,
				Type:     "text",
				Message:  msg.Message,
			}
			go notifyFastAPI(metadata, nil)
			fmt.Println("[logs] text relayed to FastAPI; keeping TCP open")
			continue // stay on same socket
		}

		// 3) Otherwise treat it as FILE metadata
		var metadata config.FileMetadata
		if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
			fmt.Println("[Error] invalid metadata JSON:", err)
			// bad frame; don't kill socket—wait for next
			continue
		}

		// 4) Create ephemeral UDP listener for this ONE transfer
		udpAddr, err := net.ResolveUDPAddr("udp", ":0")
		if err != nil {
			fmt.Println("[Error] resolve UDP addr:", err)
			continue
		}
		udpConn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			fmt.Println("[Error] listen UDP:", err)
			continue
		}
		_ = udpConn.SetReadBuffer(4 << 20)
		udpPort := udpConn.LocalAddr().(*net.UDPAddr).Port

		// 5) Tell sender which UDP port to use
		if _, err := conn.Write([]byte(fmt.Sprintf("Start:%d\n", udpPort))); err != nil {
			fmt.Println("[Error] TCP write Start:", err)
			udpConn.Close()
			continue
		}

		// 6) Receive chunks over UDP; ACKs go back on this same TCP socket
		fileDataChan := make(chan []byte, 1)
		done := make(chan struct{}, 1)

		go func() {
			combined := StartUDPReceiverConn(udpConn, metadata, conn, done)
			fileDataChan <- combined
		}()

		// 7) Wait for receive to finish (or timeout)
		select {
		case <-done:
			combined := <-fileDataChan
			if combined == nil && metadata.Size > 0 {
				fmt.Println("[logs] receive failed (nil data)")
				_, _ = conn.Write([]byte("error:receive_failed\n"))
				udpConn.Close()
				continue
			}
			// signal stop-of-acks and close UDP
			_, _ = conn.Write([]byte("stop\n"))
			udpConn.Close()

			// hand off to FastAPI (multipart or whatever your notify does)
			go notifyFastAPI(metadata, combined)
			fmt.Println("[logs] file delivered; keeping TCP open")

		case <-time.After(120 * time.Second):
			fmt.Println("[logs] timeout waiting for file data")
			udpConn.Close()
			_, _ = conn.Write([]byte("error:timeout\n"))
		}

		// loop again for next text/file on the same TCP conn
	}
}





func notifyFastAPI(metadata config.FileMetadata, combinedFileData []byte) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	_ = w.WriteField("sender", metadata.Sender)
	_ = w.WriteField("receiver", metadata.Receiver)
	_ = w.WriteField("message_type", metadata.Type)
	_ = w.WriteField("message", metadata.Message)

	// Optional file
	if len(combinedFileData) > 0 && metadata.Name != "" {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "files", metadata.Name))

		ct := metadata.Type
		if ct == "" {
			ct = "application/octet-stream"
		}
		hdr.Set("Content-Type", ct)

		fw, err := w.CreatePart(hdr)
		if err != nil {
			return fmt.Errorf("create multipart part: %w", err)
		}
		if _, err := fw.Write(combinedFileData); err != nil {
			return fmt.Errorf("write file bytes: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/go_message", config.FastAPIHost, config.FastAPIPort)

	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// include a small snippet of the body to aid debugging
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("FastAPI returned status %d: %s", resp.StatusCode, string(b))
	}

	fmt.Printf("[✓] Notified FastAPI (multipart) about %s (%d bytes, type %s)\n",
		metadata.Name, len(combinedFileData), metadata.Type)
	return nil
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