package transfer

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go_files/config"
)


func StartUDPReceiverConn(udpConn *net.UDPConn, metadata config.FileMetadata, conn net.Conn, done chan struct{}) []byte {

	fileBytes := make([]byte, metadata.Size)
	received := make([]bool, metadata.Chunks+1)
	receivedCount := 0

	idleStart := time.Now()
    maxIdle := time.Duration(config.AckTimeoutMs*2) * time.Millisecond


    for receivedCount < metadata.Chunks {
        buf := make([]byte, 44+config.ChunkSize)
        _ = udpConn.SetReadDeadline(time.Now().Add(5 * time.Second))

        n, _, err := udpConn.ReadFromUDP(buf)
        if err != nil {
            if ne, ok := err.(net.Error); ok && ne.Timeout() {

                if time.Since(idleStart) < maxIdle {
                    continue
                }
                fmt.Println("[logs] UDP idle timeout; not all chunks received")
                _, _ = conn.Write([]byte("error:timeout\n"))
                select { case done <- struct{}{}: default: }
                return nil
            }
            fmt.Println("[!] UDP read error:", err)
            _, _ = conn.Write([]byte("error:udp_read\n"))
            select { case done <- struct{}{}: default: }
            return nil
        }

        idleStart = time.Now()

        if n < 44 {
            continue
        }

		// Header layout:
		// [0:32]   SHA-256 of the (whole) file (ignored per-chunk; final check below)
		// [32:36]  chunk index (1-based)
		// [36:40]  total chunks
		// [40:44]  chunk size (payload length)
		idx := int(binary.BigEndian.Uint32(buf[32:36]))
		total := int(binary.BigEndian.Uint32(buf[36:40]))
		size := int(binary.BigEndian.Uint32(buf[40:44]))

		// Basic sanity
		if total != metadata.Chunks || idx <= 0 || idx > metadata.Chunks || size < 0 {
			continue
		}

		payload := buf[44:n]
		if len(payload) < size {
			// truncated packet; skip
			continue
		}
		// Copy into destination at offset
		offset := (idx - 1) * config.ChunkSize
		end := offset + size
		if end > len(fileBytes) || offset < 0 {
			continue
		}
		if !received[idx] {
			copy(fileBytes[offset:end], payload[:size])
			received[idx] = true
			receivedCount++
		}

		// Per-chunk ACK back on TCP control channel
		_, _ = conn.Write([]byte(fmt.Sprintf("chunk%d\n", idx)))
	}

	// Final end-to-end hash check (matches sender’s metadata.Hash)
	hasher := sha256.New()
	hasher.Write(fileBytes)
	ok := fmt.Sprintf("%x", hasher.Sum(nil)) == metadata.Hash

	// ALWAYS signal done (non-blocking) to avoid deadlocks upstream
	select { case done <- struct{}{}: default: }

	if ok {
		fmt.Println("[logs] Hash verification successful")
		return fileBytes
	}
	fmt.Println("[logs] hash mismatch")
	_, _ = conn.Write([]byte("error:hash_mismatch\n"))
	return nil
}

func StartUDPReceiver(port int, metadata config.FileMetadata, conn net.Conn, done chan bool) []byte {
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		fmt.Println("[!] resolve UDP addr:", err)
		select { case done <- true: default: }
		return nil
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	_ = udpConn.SetReadBuffer(4 << 20)
	if err != nil {
		fmt.Println("[!] listen UDP:", err)
		select { case done <- true: default: }
		return nil
	}
	defer udpConn.Close()

	doneStruct := make(chan struct{}, 1)
	data := StartUDPReceiverConn(udpConn, metadata, conn, doneStruct)

	select {
	case <-doneStruct:
		select { case done <- true: default: }
	default:
	
		select { case done <- true: default: }
	}

	return data
}
