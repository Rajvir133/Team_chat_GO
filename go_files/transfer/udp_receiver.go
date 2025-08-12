package transfer

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go_files/config"
)

// startUDPReceiver dynamically allocates a UDP port and starts receiving chunks
func startUDPReceiver(conn net.Conn, metadata config.FileMetadata) error {
	// Use dynamic port allocation to handle multiple concurrent senders
	udpAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Printf("[!] Failed to allocate UDP port: %v\n", err)
		conn.Write([]byte("ERROR:UDP_ALLOC\n"))
		return err
	}
	defer udpConn.Close()

	assignedUDPPort := udpConn.LocalAddr().(*net.UDPAddr).Port

	// Send port number back to sender for dynamic allocation
	startSignal := fmt.Sprintf("Start:%d\n", assignedUDPPort)
	log.Printf("[📤] Sending Start signal: %s", strings.TrimSpace(startSignal))
	conn.Write([]byte(startSignal))
	log.Printf("[✓] Allocated UDP port %d for %s (supports concurrent transfers)\n", assignedUDPPort, metadata.Name)

	recvDir := "received_media"
	_ = os.MkdirAll(recvDir, 0755)
	filePath := filepath.Join(recvDir, metadata.Name)

	return receiveUDPChunks(conn, metadata, udpConn, filePath)
}

func receiveUDPChunks(conn net.Conn, metadata config.FileMetadata, udpConn *net.UDPConn, filePath string) error {
	chunks := make(map[uint32][]byte)
	expectedChunks := uint32(metadata.Chunks)
	buf := make([]byte, config.ChunkSize+72) // SHA256(32) + index(4) + total(4) + size(4) + data

	// Enhanced logging: Track start time and progress
	startTime := time.Now()
	log.Printf("[📥] Starting to receive %d chunks for file: %s (%d bytes)",
		expectedChunks, metadata.Name, metadata.Size)

	// Set a timeout for UDP operations
	udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))

	for {
		n, _, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Printf("[⏰] UDP read timeout - no chunks received in 30 seconds")
				log.Printf("[❌] Transfer failed: %s", err)
				return fmt.Errorf("UDP read timeout: %v", err)
			}
			return fmt.Errorf("UDP read error: %v", err)
		}

		// Reset timeout for next read
		udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))

		if n < 44 {
			log.Printf("[⚠️] Received packet too small (%d bytes), expected at least 44 bytes", n)
			continue // too small to be valid
		}

		hash := buf[0:32]
		index := binary.BigEndian.Uint32(buf[32:36])
		total := binary.BigEndian.Uint32(buf[36:40])
		size := binary.BigEndian.Uint32(buf[40:44])
		data := buf[44 : 44+size]

		log.Printf("[📦] Processing chunk %d/%d (size: %d bytes, total: %d)",
			index, total, size, n)

		_ = hash  // Chunk-level hash unused here
		_ = total // We rely on metadata.Chunks

		chunks[index] = append([]byte(nil), data...)

		// Enhanced logging: Track chunk reception progress
		log.Printf("[📥] Chunk %d/%d received (%d bytes) - Progress: %d/%d (%.1f%%)",
			index, expectedChunks, size, len(chunks), expectedChunks,
			float64(len(chunks))/float64(expectedChunks)*100)

		// Send per-chunk ACK with timing
		ackStartTime := time.Now()
		ackMsg := fmt.Sprintf("chunk%d received\n", index)
		_, err = conn.Write([]byte(ackMsg))
		ackDuration := time.Since(ackStartTime)

		if err != nil {
			log.Printf("[⚠️] Failed to send ACK for chunk %d: %v", index, err)
		} else {
			log.Printf("[✅] ACK sent for chunk %d/%d in %v", index, expectedChunks, ackDuration)
		}

		if uint32(len(chunks)) == expectedChunks {
			totalReceiveTime := time.Since(startTime)
			log.Printf("[✓] All %d chunks received for %s in %v",
				expectedChunks, metadata.Name, totalReceiveTime)

			// Reassemble file
			log.Printf("[🔧] Reassembling file from %d chunks...", expectedChunks)
			reassembleStartTime := time.Now()

			var fileBuffer bytes.Buffer
			for i := uint32(1); i <= expectedChunks; i++ {
				fileBuffer.Write(chunks[i])
			}

			reassembleDuration := time.Since(reassembleStartTime)
			log.Printf("[✓] File reassembly completed in %v", reassembleDuration)

			// Verify hash
			log.Printf("[🔍] Verifying file hash...")
			verifyStartTime := time.Now()
			fileHash := sha256.Sum256(fileBuffer.Bytes())
			verifyDuration := time.Since(verifyStartTime)

			if fmt.Sprintf("%x", fileHash[:]) != metadata.Hash {
				log.Printf("[❌] Hash verification failed for %s", metadata.Name)
				conn.Write([]byte("ERROR:HASH_MISMATCH\n"))
				return fmt.Errorf("hash mismatch for %s", metadata.Name)
			}
			log.Printf("[✓] Hash verification passed in %v", verifyDuration)

			// Decompress if needed
			var finalData []byte
			if !config.NoCompressionTypes[metadata.Type] {
				log.Printf("[🗜️] Decompressing file data...")
				decompressStartTime := time.Now()

				zr, err := zlib.NewReader(bytes.NewReader(fileBuffer.Bytes()))
				if err != nil {
					return fmt.Errorf("zlib reader error: %v", err)
				}
				finalData, err = io.ReadAll(zr)
				zr.Close()
				if err != nil {
					return fmt.Errorf("zlib read error: %v", err)
				}

				decompressDuration := time.Since(decompressStartTime)
				log.Printf("[✓] Decompression completed in %v (original: %d bytes, final: %d bytes)",
					decompressDuration, len(fileBuffer.Bytes()), len(finalData))
			} else {
				finalData = fileBuffer.Bytes()
				log.Printf("[ℹ️] No decompression needed for file type: %s", metadata.Type)
			}

			// Save file
			log.Printf("[💾] Saving file to %s...", filePath)
			saveStartTime := time.Now()

			if err := os.WriteFile(filePath, finalData, 0644); err != nil {
				return fmt.Errorf("file write error: %v", err)
			}

			saveDuration := time.Since(saveStartTime)
			log.Printf("[💾] File saved successfully in %v", saveDuration)

			// Final ACK to signal completion
			log.Printf("[🎯] Sending final completion ACK...")
			conn.Write([]byte("stop\n"))
			log.Printf("[✓] Final ACK sent - Transfer complete!")
			break
		}
	}
	return nil
}
