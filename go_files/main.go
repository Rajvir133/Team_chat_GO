package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "fmt"
    "net"
    "net/http"
    "sync"
    "time"
    "encoding/base64"
	"compress/zlib"
	"crypto/sha256"
	"io"
)

// Message structure
type Message struct {
    Sender      string        `json:"sender"`
    Receiver    string        `json:"receiver"`
    MessageType string        `json:"message_type"`
    Message     string        `json:"message"`
    Payload     []FilePayload `json:"payload"`
}


type FilePayload struct {
    Name string `json:"name"`
    Type string `json:"type"`
    Data string `json:"data"` // Decoded bytearray (from base64)
}

const CHUNK_SIZE = 52768





func decodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}










func handleTCPConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Step 1: Read metadata (ends with \n)
	metaLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("[!] Failed to read metadata:", err)
		return
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		fmt.Println("[!] Invalid metadata format:", err)
		return
	}

	messageType, ok := metadata["message_type"].(string)
	if !ok {
		fmt.Println("[!] message_type missing or not a string")
		return
	}

	if messageType != "image" && messageType != "video" {
		// This is a text message, forward as-is
		msg := Message{}
		if err := json.Unmarshal([]byte(metaLine), &msg); err != nil {
			fmt.Println("[!] Invalid text message format:", err)
			return
		}
		jsonData, _ := json.Marshal(msg)
		_, err = http.Post("http://localhost:5000/receive", "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Println("[!] Failed to forward text to FastAPI:", err)
		} else {
			fmt.Printf("[✓] Text from %s received and forwarded.\n", msg.Sender)
		}
		return
	}

	name := metadata["name"].(string)
	fileType := metadata["type"].(string)
	size := int(metadata["size"].(float64)) 
	hash := metadata["hash"].(string)
	sender := metadata["sender"].(string)
	receiver := metadata["receiver"].(string)
	// messageType := metadata["message_type"].(string) // This line is now redundant

	// Step 2: Read compressed data (exact size)
	compressedData := make([]byte, size)
	read := 0
	for read < size {
		n, err := reader.Read(compressedData[read:])
		if err != nil {
			fmt.Println("[!] Error reading data chunk:", err)
			return
		}
		read += n
	}

	// Step 3: Validate SHA-256 hash
	calculatedHash := fmt.Sprintf("%x", sha256.Sum256(compressedData))
	if calculatedHash != hash {
		fmt.Printf("[✗] Hash mismatch! Expected: %s, Got: %s\n", hash, calculatedHash)
		return
	}

	// Step 4: Decompress using zlib only if not already-compressed type
	alreadyCompressed := fileType == "image/jpeg" || fileType == "image/jpg" || fileType == "image/png" || fileType == "image/gif" || fileType == "video/mp4" || fileType == "video/avi" || fileType == "video/mov"
	var decompressedData []byte
	if alreadyCompressed {
		decompressedData = compressedData
	} else {
		zlibReader, err := zlib.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			fmt.Println("[!] Zlib decompress error:", err)
			return
		}
		defer zlibReader.Close()

		decompressedData, err = io.ReadAll(zlibReader)
		if err != nil {
			fmt.Println("[!] Failed to read decompressed data:", err)
			return
		}
	}

	// Step 5: Repackage as base64 for FastAPI
	encoded := encodeBase64(decompressedData)

	payload := []FilePayload{
		{
			Name: name,
			Type: fileType,
			Data: encoded,
		},
	}

	msg := Message{
		Sender:      sender,
		Receiver:    receiver,
		MessageType: messageType,
		Payload:     payload,
	}

	// Step 6: Send to FastAPI
	jsonData, _ := json.Marshal(msg)
	_, err = http.Post("http://localhost:5000/receive", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Println("[!] Failed to forward to FastAPI:", err)
	} else {
		fmt.Printf("[✓] %s from %s received and forwarded.\n", messageType, sender)
	}
}









// Handle scan API — finds devices with port 9000 open
func handleScan(w http.ResponseWriter, r *http.Request) {
    subnet := "192.168.29."
    port := "9000"
    timeout := time.Millisecond * 300

    var wg sync.WaitGroup
    var mu sync.Mutex
    var reachable []string

    for i := 1; i <= 255; i++ {
        ip := fmt.Sprintf("%s%d", subnet, i)
        wg.Add(1)
        go func(ip string) {
            defer wg.Done()
            conn, err := net.DialTimeout("tcp", ip+":"+port, timeout)
            if err == nil {
                mu.Lock()
                reachable = append(reachable, ip)
                mu.Unlock()
                conn.Close()
            }
        }(ip)
    }

    wg.Wait()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string][]string{"devices": reachable})
}











func handleSend(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	conn, err := net.Dial("tcp", msg.Receiver+":9000")
	if err != nil {
		http.Error(w, "Failed to connect to device: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Use buffered writer for efficient chunked transfer
	bufWriter := bufio.NewWriter(conn)

	if msg.MessageType == "image" || msg.MessageType == "video" {
		files := msg.Payload

		for _, file := range files {
			// Set write deadline for each file
			conn.SetWriteDeadline(time.Now().Add(60 * time.Second))

			// Decode base64
			rawBytes, err := decodeBase64(file.Data)
			if err != nil {
				http.Error(w, "Base64 decode error", http.StatusBadRequest)
				return
			}

			// Skip compression for already-compressed types
			alreadyCompressed := file.Type == "image/jpeg" || file.Type == "image/jpg" || file.Type == "image/png" || file.Type == "image/gif" || file.Type == "video/mp4" || file.Type == "video/avi" || file.Type == "video/mov"
			var compressedData []byte
			if alreadyCompressed {
				compressedData = rawBytes
			} else {
				// Compress using zlib
				var compressed bytes.Buffer
				zlibWriter := zlib.NewWriter(&compressed)
				_, err = zlibWriter.Write(rawBytes)
				if err != nil {
					http.Error(w, "Compression error", http.StatusInternalServerError)
					return
				}
				zlibWriter.Close()
				compressedData = compressed.Bytes()
			}

			// Hash
			hash := sha256.Sum256(compressedData)
			hashHex := fmt.Sprintf("%x", hash[:])

			// Metadata (you can enhance it later)
			metadata := map[string]interface{}{
				"name": file.Name,
				"type": file.Type,
				"size": len(compressedData),
				"hash": hashHex,
				"sender": msg.Sender,
				"receiver": msg.Receiver,
				"message_type": msg.MessageType,
			}
			metaBytes, _ := json.Marshal(metadata)
			bufWriter.Write(append(metaBytes, '\n'))

			// Send compressed file in chunks using buffered writer
			sent := 0
			for sent < len(compressedData) {
				end := sent + CHUNK_SIZE
				if end > len(compressedData) {
					end = len(compressedData)
				}
				bufWriter.Write(compressedData[sent:end])
				sent = end
			}
			// Flush after each file
			bufWriter.Flush()
		}

		// Response after sending
		elapsed := time.Since(start)
		fmt.Printf("[→] Sent compressed file to %s: %s (%d bytes)\n", msg.Receiver, msg.MessageType, len(files))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "sent",
			"elapsed_ms":     elapsed.Milliseconds(),
			"elapsed_string": elapsed.String(),
		})
		return
	}

	jsonData, _ := json.Marshal(msg)
	_, err = conn.Write(append(jsonData, '\n'))
	if err != nil {
		http.Error(w, "Failed to send to device", http.StatusInternalServerError)
		return
	}

	elapsed := time.Since(start)
	fmt.Printf("[→] Sent to device %s: %s\n", msg.Receiver, msg.MessageType)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "sent",
		"elapsed_ms":     elapsed.Milliseconds(),
		"elapsed_string": elapsed.String(),
	})
}















// Optional: Forward HTTP POST /incoming to FastAPI
func handleIncoming(w http.ResponseWriter, r *http.Request) {
    var msg Message
    if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }

    jsonData, err := json.Marshal(msg)
    if err != nil {
        http.Error(w, "Marshal error", http.StatusInternalServerError)
        return
    }

    resp, err := http.Post("http://localhost:5000/receive", "application/json", bytes.NewBuffer(jsonData))
    if err != nil {
        http.Error(w, "Failed to forward to FastAPI", http.StatusInternalServerError)
        return
    }

    fmt.Println("[✓] Forwarded to FastAPI:", resp.Status)
    defer resp.Body.Close()
    w.Write([]byte(`{"status": "forwarded"}`))
}

func main() {
    // Start HTTP server (for FastAPI to call)
    go func() {
        http.HandleFunc("/send", handleSend)
        http.HandleFunc("/scan", handleScan)
        http.HandleFunc("/incoming", handleIncoming)
        fmt.Println("🌐 HTTP server listening on port 8080...")
        http.ListenAndServe(":8080", nil)
    }()

    // Start TCP server (for other devices to send messages)
    go func() {
        listener, err := net.Listen("tcp", ":9000")
        if err != nil {
            panic(err)
        }
        fmt.Println("🔌 TCP server listening on port 9000...")
        for {
            conn, err := listener.Accept()
            if err != nil {
                fmt.Println("[!] TCP accept error:", err)
                continue
            }
            go handleTCPConnection(conn)
        }
    }()

    // Block forever
    select {}
}
