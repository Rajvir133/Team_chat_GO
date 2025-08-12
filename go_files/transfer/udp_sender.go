package transfer

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go_files/config"
	"log"
	"net"
	"strings"
	"time"
)

// SendUDPChunksFromBytes sends already-loaded data over UDP
func SendUDPChunks(conn net.Conn, metadata config.FileMetadata, data []byte) error {
	return sendUDPChunksCore(conn, metadata, data)
}

// Core function that handles compression, hashing, chunking, and UDP sending
func sendUDPChunksCore(conn net.Conn, metadata config.FileMetadata, data []byte) error {
	// Compress if not already compressed type
	if !config.NoCompressionTypes[metadata.Type] {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write(data); err != nil {
			return fmt.Errorf("zlib write error: %v", err)
		}
		zw.Close()
		data = buf.Bytes()
	}

	// Calculate hash
	hash := sha256.Sum256(data)
	metadata.Hash = fmt.Sprintf("%x", hash[:])

	// Split into chunks
	chunks := splitIntoChunks(data, config.ChunkSize)
	metadata.Chunks = len(chunks)

	// Send metadata over TCP
	metaBytes, _ := json.Marshal(metadata)
	log.Printf("[📤] Sending metadata over TCP: %s (%d bytes, %d chunks)",
		metadata.Name, metadata.Size, metadata.Chunks)
	conn.Write(append(metaBytes, '\n'))

	// Wait for UDP start signal
	log.Printf("[⏳] Waiting for UDP start signal from receiver...")
	tcpAck := make([]byte, 64)
	n, err := conn.Read(tcpAck)
	if err != nil {
		return fmt.Errorf("failed to read UDP start signal: %v", err)
	}
	ackMsg := string(tcpAck[:n])
	log.Printf("[📥] Received TCP response: %q", strings.TrimSpace(ackMsg))

	// Parse the dynamic UDP port from response
	if len(ackMsg) < 6 || ackMsg[:6] != "Start:" {
		return fmt.Errorf("unexpected start message: %s", ackMsg)
	}

	var udpPort int
	fmt.Sscanf(ackMsg, "Start:%d\n", &udpPort)
	log.Printf("[→] Receiver allocated UDP port: %d", udpPort)

	udpAddr := &net.UDPAddr{IP: net.ParseIP(metadata.Receiver), Port: udpPort}
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("UDP dial error: %v", err)
	}
	defer udpConn.Close()

	// Enhanced logging: Track start time and progress
	startTime := time.Now()
	log.Printf("[📤] Starting transfer of %s (%d chunks, %d bytes) to %s:%d",
		metadata.Name, len(chunks), len(data), metadata.Receiver, udpPort)

	// Send chunks with detailed logging
	for idx, chunk := range chunks {
		chunkStartTime := time.Now()

		buf := new(bytes.Buffer)
		buf.Write(hash[:])                                       // File hash
		binary.Write(buf, binary.BigEndian, uint32(idx+1))       // Chunk index
		binary.Write(buf, binary.BigEndian, uint32(len(chunks))) // Total chunks
		binary.Write(buf, binary.BigEndian, uint32(len(chunk)))  // Chunk size
		buf.Write(chunk)                                         // Chunk data

		if _, err := udpConn.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("UDP send error for chunk %d: %v", idx+1, err)
		}

		// Log chunk sent with progress
		chunkDuration := time.Since(chunkStartTime)
		log.Printf("[📦] Chunk %d/%d sent (%d bytes) in %v",
			idx+1, len(chunks), len(chunk), chunkDuration)

		// Wait for TCP acknowledgment for this chunk
		ackBuffer := make([]byte, 64)
		ackStartTime := time.Now()
		n, err := conn.Read(ackBuffer)
		ackDuration := time.Since(ackStartTime)

		if err != nil {
			return fmt.Errorf("failed to read ACK for chunk %d: %v", idx+1, err)
		}

		ackMsg := string(ackBuffer[:n])
		log.Printf("[✅] Chunk %d/%d ACK received: %s (in %v)",
			idx+1, len(chunks), strings.TrimSpace(ackMsg), ackDuration)

		// Verify ACK format
		expectedAck := fmt.Sprintf("chunk%d received", idx+1)
		if !strings.Contains(ackMsg, expectedAck) {
			log.Printf("[⚠️] Unexpected ACK for chunk %d: expected '%s', got '%s'",
				idx+1, expectedAck, strings.TrimSpace(ackMsg))
		}

		time.Sleep(10 * time.Millisecond) // pacing
	}

	totalDuration := time.Since(startTime)
	log.Printf("[✓] All %d chunks sent successfully to %s:%d in %v",
		len(chunks), metadata.Receiver, udpPort, totalDuration)

	// Wait for final TCP ACK
	finalAck := make([]byte, 64)
	n, err = conn.Read(finalAck)
	if err != nil {
		return fmt.Errorf("failed to read final ACK: %v", err)
	}

	finalMsg := string(finalAck[:n])
	if finalMsg == "ERROR:HASH_MISMATCH\n" {
		return fmt.Errorf("hash mismatch reported by receiver")
	}
	if finalMsg != "stop\n" {
		return fmt.Errorf("unexpected final ACK: %s", finalMsg)
	}

	log.Printf("[🎯] Final ACK received: %s", strings.TrimSpace(finalMsg))
	log.Println("[✓] File transfer complete")
	return nil
}

// splitIntoChunks splits data into fixed-size chunks
func splitIntoChunks(data []byte, size int) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(data); i += size {
		end := i + size
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return chunks
}
