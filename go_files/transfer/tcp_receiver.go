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
	"time"
	"net/textproto"

	"go_files/config"
)

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
	defer conn.Close()
	reader := bufio.NewReader(conn)

	metaLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("[!] Failed to read metadata:", err)
		return
	}

	// Try as "text" path first (config.Message)
	var msg config.Message
	if err := json.Unmarshal([]byte(metaLine), &msg); err == nil {
		if msg.MessageType == "text" {
			metadata := config.FileMetadata{
				Sender:   msg.Sender,
				Receiver: msg.Receiver,
				Type:     msg.MessageType,
				Message:  msg.Message,
				Name:     "",
				Size:     0,
				Chunks:   0,
				Hash:     "",
			}

			go notifyFastAPI(metadata, nil)
			fmt.Println("[logs] Text message sent to FastAPI")
			return
		}
	}

	// Otherwise parse as file metadata
	var metadata config.FileMetadata
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		fmt.Println("[Error] Invalid metadata:", err)
		return
	}

	fmt.Printf("[logs] Incoming file : %s , %d , %d, %s , %s\n",
		metadata.Name, metadata.Size, metadata.Chunks, metadata.Sender, metadata.Type)

	udpAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		fmt.Println("[Error] resolve UDP addr:", err)
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("[Error] listen UDP:", err)
		return
	}
	_ = udpConn.SetReadBuffer(4 << 20)
	
	udpPort := udpConn.LocalAddr().(*net.UDPAddr).Port

	fmt.Printf("[TCP] start : %d\n", udpPort)
	if _, err := conn.Write([]byte(fmt.Sprintf("Start:%d\n", udpPort))); err != nil {
		fmt.Println("[Error] failed to write start:", err)
		udpConn.Close()
		return
	}

	// Deadlock-safe: buffered result + signal-only done
	fileDataChan := make(chan []byte, 1)
	done := make(chan struct{}, 1)

	go func() {
		fileBytes := StartUDPReceiverConn(udpConn, metadata, conn, done)
		fileDataChan <- fileBytes
	}()

	select {
	case <-done:
		combinedFileData := <-fileDataChan
		if combinedFileData == nil && metadata.Size > 0 {
			fmt.Println("[logs] receive failed (nil data)")
			_, _ = conn.Write([]byte("error:receive_failed\n"))
			udpConn.Close()
			return
		}

		fmt.Println("[logs] all chunk received")
		_, _ = conn.Write([]byte("stop\n"))
		fmt.Println("[TCP] sending stop")

		udpConn.Close()

		go notifyFastAPI(metadata, combinedFileData)

	case <-time.After(120 * time.Second):
		fmt.Println("[logs] Timeout waiting for file")
		udpConn.Close()
		_, _ = conn.Write([]byte("error:timeout\n"))
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