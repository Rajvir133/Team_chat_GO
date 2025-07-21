package main

import (
    "bufio"
    "bytes"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "net"
    "net/http"
    "sync"
    "time"
	"compress/zlib"
	"crypto/sha256"
	"io"
	"math"
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
    Name        string      `json:"name"`
    Type        string      `json:"type"`
    Size        int         `json:"size"`
    Hash        string      `json:"hash"`
    Sender      string      `json:"sender"`
    Receiver    string      `json:"receiver"`
    TotalChunks int         `json:"total_chunks"`
    Data        interface{} `json:"data,omitempty"` // Raw bytes
    MessageType string      `json:"message_type,omitempty"`
}

const CHUNK_SIZE = 16096  // 4KB chunks for UDP transfer
const UDP_PORT = 9001

// sendAck sends acknowledgment for received chunks
func sendAck(conn net.Conn, batch []int, totalChunks int, received map[int]bool) {
	// Find missing chunks in the expected range
	missing := []int{}
	expectedRange := batch
	if len(batch) == 3 {
		// For batches of 3, check the expected range
		start := batch[0] - (batch[0] % 3)
		end := start + 3
		if end > totalChunks {
			end = totalChunks
		}
		expectedRange = []int{}
		for i := start; i < end; i++ {
			expectedRange = append(expectedRange, i)
		}
	}
	
	for _, idx := range expectedRange {
		if !received[idx] {
			missing = append(missing, idx)
		}
	}
	
	ack := map[string]interface{}{
		"received": batch,
		"missing":  missing,
	}
	
	ackBytes, _ := json.Marshal(ack)
	conn.Write(append(ackBytes, '\n'))
	
	fmt.Printf("[📤] ACK sent: received=%v, missing=%v\n", batch, missing)
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

	// For media files, receive chunks via UDP
	name := metadata["name"].(string)
	fileType := metadata["type"].(string)
	// size := int(metadata["size"].(float64)) 
	hash := metadata["hash"].(string)
	sender := metadata["sender"].(string)
	receiver := metadata["receiver"].(string)
	totalChunks := int(metadata["total_chunks"].(float64))

	// Step 2: Receive file chunks via UDP
	udpAddr, _ := net.ResolveUDPAddr("udp", ":9001")
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("[!] Failed to listen on UDP 9001:", err)
		return
	}
	defer udpConn.Close()

	chunkData := make([][]byte, totalChunks)
	received := make(map[int]bool)
	
	fmt.Printf("[📥] Waiting for %d UDP chunks for %s...\n", totalChunks, name)
	
	// Receive chunks in batches and send ACKs
	batch := []int{}
	
	for len(received) < totalChunks {
		// Set timeout for each UDP read
		udpConn.SetReadDeadline(time.Now().Add(time.Second * 5))
		
		// Read index (4 bytes)
		indexBytes := make([]byte, 4)
		_, err := io.ReadFull(udpConn, indexBytes)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Timeout - send ACK for what we have
				if len(batch) > 0 {
					sendAck(conn, batch, totalChunks, received)
					batch = []int{}
				}
				continue
			}
			fmt.Println("[!] UDP read index error:", err)
			continue
		}
		
		// Read size (4 bytes)
		sizeBytes := make([]byte, 4)
		_, err = io.ReadFull(udpConn, sizeBytes)
		if err != nil {
			fmt.Println("[!] UDP read size error:", err)
			continue
		}
		
		// Parse index and size
		chunkIndex := int(binary.BigEndian.Uint32(indexBytes))
		chunkSize := int(binary.BigEndian.Uint32(sizeBytes))
		
		// Debug: Log raw bytes to identify corruption
		fmt.Printf("[🔍] Raw index bytes: %v, Raw size bytes: %v\n", indexBytes, sizeBytes)
		fmt.Printf("[🔍] Parsed index: %d, size: %d\n", chunkIndex, chunkSize)
		
		// Validate chunk index and size
		if chunkIndex < 0 || chunkIndex >= totalChunks {
			fmt.Printf("[!] Invalid chunk index: %d (expected 0-%d)\n", chunkIndex, totalChunks-1)
			continue
		}
		
		if chunkSize <= 0 || chunkSize > CHUNK_SIZE {
			fmt.Printf("[!] Invalid chunk size: %d (expected 1-%d)\n", chunkSize, CHUNK_SIZE)
			continue
		}
		
		// Read chunk data
		chunkDataBytes := make([]byte, chunkSize)
		_, err = io.ReadFull(udpConn, chunkDataBytes)
		if err != nil {
			fmt.Println("[!] UDP read data error:", err)
			continue
		}
		
		if !received[chunkIndex] {
			chunkData[chunkIndex] = chunkDataBytes
			received[chunkIndex] = true
			batch = append(batch, chunkIndex)
			fmt.Printf("[📦] Received chunk %d/%d (size: %d bytes)\n", chunkIndex+1, totalChunks, chunkSize)
		}
		
		// Send ACK when we have 3 chunks or all chunks received
		if len(batch) == 3 || len(received) == totalChunks {
			sendAck(conn, batch, totalChunks, received)
			batch = []int{}
		}
	}

	// Step 3: Reconstruct file
	var fullData []byte
	for i := 0; i < totalChunks; i++ {
		if i < len(chunkData) && chunkData[i] != nil {
			fullData = append(fullData, chunkData[i]...)
		} else {
			fmt.Printf("[!] Missing chunk %d during reconstruction\n", i)
			return
		}
	}

	// Step 4: Validate hash
	calculatedHash := fmt.Sprintf("%x", sha256.Sum256(fullData))
	if calculatedHash != hash {
		fmt.Printf("[✗] Hash mismatch! Expected: %s, Got: %s\n", hash, calculatedHash)
		return
	}

	// Step 5: Decompress if needed
	alreadyCompressed := fileType == "image/jpeg" || fileType == "image/jpg" || fileType == "image/png" || fileType == "image/gif" || fileType == "video/mp4" || fileType == "video/avi" || fileType == "video/mov"
	var decompressedData []byte
	if alreadyCompressed {
		decompressedData = fullData
	} else {
		zlibReader, err := zlib.NewReader(bytes.NewReader(fullData))
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

	// Step 6: Create payload with raw bytes converted to list for JSON serialization
	// Convert bytes to list for JSON compatibility
	byteList := make([]interface{}, len(decompressedData))
	for i, b := range decompressedData {
		byteList[i] = int(b)
	}
	
	payload := []FilePayload{
		{
			Name: name,
			Type: fileType,
			Data: byteList, // Converted to list for JSON serialization
		},
	}

	msg := Message{
		Sender:      sender,
		Receiver:    receiver,
		MessageType: messageType,
		Payload:     payload,
	}

	// Step 7: Send to FastAPI
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

	bufWriter := bufio.NewWriter(conn)

	if msg.MessageType == "image" || msg.MessageType == "video" {
		files := msg.Payload

		for _, file := range files {
			// Handle raw bytes
			var rawBytes []byte
			if strData, ok := file.Data.(string); ok {
				// Convert string to bytes
				rawBytes = []byte(strData)
			} else if byteData, ok := file.Data.([]byte); ok {
				// Use bytes directly
				rawBytes = byteData
			} else if listData, ok := file.Data.([]interface{}); ok {
				// Convert list to bytes
				rawBytes = make([]byte, len(listData))
				for i, v := range listData {
					if num, ok := v.(float64); ok {
						rawBytes[i] = byte(num)
					}
				}
			} else {
				http.Error(w, "Invalid data type", http.StatusBadRequest)
				return
			}

			// Skip compression for already-compressed types
			alreadyCompressed := file.Type == "image/jpeg" || file.Type == "image/jpg" || file.Type == "image/png" || file.Type == "image/gif" || file.Type == "video/mp4" || file.Type == "video/avi" || file.Type == "video/mov"
			var compressedData []byte
			if alreadyCompressed {
				compressedData = rawBytes
			} else {
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

			totalChunks := int(math.Ceil(float64(len(compressedData)) / float64(CHUNK_SIZE)))
			hash := sha256.Sum256(compressedData)
			hashHex := fmt.Sprintf("%x", hash[:])

			// Prepare metadata (NO Data field)
			metadata := FilePayload{
				Name:        file.Name,
				Type:        file.Type,
				Size:        len(compressedData),
				Hash:        hashHex,
				Sender:      msg.Sender,
				Receiver:    msg.Receiver,
				TotalChunks: totalChunks,
				MessageType: msg.MessageType,
			}

			metaBytes, _ := json.Marshal(metadata)
			bufWriter.Write(append(metaBytes, '\n'))
			bufWriter.Flush()

			// --- UDP SENDER LOGIC ---
			udpAddr, err := net.ResolveUDPAddr("udp", msg.Receiver+":9001")
			if err != nil {
				http.Error(w, "Failed to resolve UDP address: "+err.Error(), http.StatusInternalServerError)
				return
			}
			udpConn, err := net.DialUDP("udp", nil, udpAddr)
			if err != nil {
				http.Error(w, "Failed to dial UDP: "+err.Error(), http.StatusInternalServerError)
				return
			}
			defer udpConn.Close()

			// Send chunks via UDP in batches of 3
			fmt.Printf("[📤] Sending %d chunks via UDP for %s...\n", totalChunks, file.Name)
			
			// Prepare all chunks first
			chunkMap := make(map[int][]byte)
			for i := 0; i < totalChunks; i++ {
				start := i * CHUNK_SIZE
				end := start + CHUNK_SIZE
				if end > len(compressedData) {
					end = len(compressedData)
				}
				chunkMap[i] = compressedData[start:end]
			}
			
			sentChunks := make(map[int]bool)
			
			// Send in batches of 3
			for batchStart := 0; batchStart < totalChunks; batchStart += 3 {
				batchEnd := batchStart + 3
				if batchEnd > totalChunks {
					batchEnd = totalChunks
				}
				
				// Send up to 3 chunks
				for i := batchStart; i < batchEnd; i++ {
					// Create binary packet: [4 bytes index][4 bytes size][raw data]
					indexBytes := make([]byte, 4)
					sizeBytes := make([]byte, 4)
					binary.BigEndian.PutUint32(indexBytes, uint32(i))
					binary.BigEndian.PutUint32(sizeBytes, uint32(len(chunkMap[i])))
					
					// Send binary packet
					packet := append(indexBytes, sizeBytes...)
					packet = append(packet, chunkMap[i]...)
					udpConn.Write(packet)
					
					sentChunks[i] = true
					fmt.Printf("[📦] Sent chunk %d/%d (size: %d bytes)\n", i+1, totalChunks, len(chunkMap[i]))
				}
				
				// Wait for ACK on TCP
				ackLine, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					fmt.Println("[!] Failed to read ACK:", err)
					return
				}
				
				var ackMsg struct {
					Received []int `json:"received"`
					Missing  []int `json:"missing"`
				}
				json.Unmarshal([]byte(ackLine), &ackMsg)
				
				// Resend missing chunks if any
				for _, missingIdx := range ackMsg.Missing {
					// Create binary packet: [4 bytes index][4 bytes size][raw data]
					indexBytes := make([]byte, 4)
					sizeBytes := make([]byte, 4)
					binary.BigEndian.PutUint32(indexBytes, uint32(missingIdx))
					binary.BigEndian.PutUint32(sizeBytes, uint32(len(chunkMap[missingIdx])))
					
					// Send binary packet
					packet := append(indexBytes, sizeBytes...)
					packet = append(packet, chunkMap[missingIdx]...)
					udpConn.Write(packet)
					
					fmt.Printf("[🔄] Resent chunk %d/%d\n", missingIdx+1, totalChunks)
				}
				
				// If all received, continue to next batch
				if len(ackMsg.Missing) == 0 {
					fmt.Printf("[✅] Batch %d-%d completed successfully\n", batchStart+1, batchEnd)
				}
			}
			// --- END UDP SENDER LOGIC ---
		}

		elapsed := time.Since(start)
		fmt.Printf("[→] Sent metadata to %s: %s (%d files)\n", msg.Receiver, msg.MessageType, len(files))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "metadata sent, UDP chunks sent",
			"elapsed_ms":     elapsed.Milliseconds(),
			"elapsed_string": elapsed.String(),
		})
		return
	}

	// Default text message (fallback for non-media)
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
