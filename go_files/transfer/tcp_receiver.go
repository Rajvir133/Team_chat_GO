package transfer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"time"

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
            fmt.Println("[Eror] TCP accept error:", err)
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
		fmt.Printf("message_type : %s\n", msg.MessageType)
		if msg.MessageType == "text" {
			metadata := config.FileMetadata{
				Sender:   msg.Sender,
				Receiver: msg.Receiver,
				Type:     msg.MessageType, // "text"
				Message:  msg.Message,
				Name:     "",
				Size:     0,
				Chunks:   0,
				Hash:     "",
			}
			// Notify FastAPI (multipart with only fields, no file)
			go notifyFastAPI(metadata, nil)
			fmt.Println("[logs] Text message sent to FastAPI")
			return
		}
	}

	// Otherwise parse as file metadata
	var metadata config.FileMetadata
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		fmt.Println("[!] Invalid metadata:", err)
		return
	}

	fmt.Printf("[logs] Incoming file : %s , %d , %d, %s , %s\n",
		metadata.Name, metadata.Size, metadata.Chunks, metadata.Sender, metadata.Type)

	udpAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		fmt.Println("[!] resolve UDP addr:", err)
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("[!] listen UDP:", err)
		return
	}
	_ = udpConn.SetReadBuffer(4 << 20)
	
	udpPort := udpConn.LocalAddr().(*net.UDPAddr).Port

	fmt.Printf("[TCP] start : %d\n", udpPort)
	if _, err := conn.Write([]byte(fmt.Sprintf("Start:%d\n", udpPort))); err != nil {
		fmt.Println("[!] failed to write start:", err)
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
		fw, err := w.CreateFormFile("files", metadata.Name)
		if err != nil {
			fmt.Printf("create form file error: %v\n", err)
			return fmt.Errorf("create form file: %w", err)
		}
		if _, err := fw.Write(combinedFileData); err != nil {
			fmt.Printf("write file bytes error: %v\n", err)
			return fmt.Errorf("write file bytes: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		fmt.Printf("multipart close error: %v\n", err)
		return fmt.Errorf("close multipart writer: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/go_message", config.FastAPIHost, config.FastAPIPort)
	resp, err := http.Post(url, w.FormDataContentType(), &body)
	if err != nil {
		fmt.Printf("HTTP post error: %v\n", err)
		return fmt.Errorf("HTTP post error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("FastAPI returned status %d\n", resp.StatusCode)
		return fmt.Errorf("FastAPI returned status %d", resp.StatusCode)
	}

	fmt.Printf("[✓] Notified FastAPI (multipart) about %s (%d bytes)\n", metadata.Name, len(combinedFileData))
	return nil
}
