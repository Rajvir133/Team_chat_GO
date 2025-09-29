package transfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"bytes"
	"fmt"
	"net"
	"strings"
	"time"

	"go_files/config"
)


func StartUDPReceiverConn(udpConn *net.UDPConn, metadata config.FileMetadata, conn net.Conn, done chan struct{}) []byte {
	defer func() { select { case done <- struct{}{}: default: } }()

	// Unified protocol:
	// Datagram = [16B xferId] [u32 seq (0-based)] [u16 len] [payload]
	// ACK      = "ACK_UDP:<xferId>:<seq>\n"
	// Finish   = sender later writes "END_UDP:<xferId>" on TCP (we don't need to send anything)

	idBytes, err := hex.DecodeString(strings.ToLower(metadata.XferId))
	if err != nil || len(idBytes) != 16 {
		fmt.Println("[udp] invalid xferId")
		_, _ = conn.Write([]byte("error:invalid_xferid\n"))
		return nil
	}

	fileBytes := make([]byte, metadata.Size)
	maxPayload := config.ChunkSize
	expected := metadata.Chunks
	received := make([]bool, expected) // 0-based
	receivedCount := 0

	rxBuf := make([]byte, 16+4+2+maxPayload)
	ackW := bufio.NewWriter(conn)
	defer ackW.Flush()

	idleStart := time.Now()
	idleTimeout := 20 * time.Second

	for {
		_ = udpConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _, err := udpConn.ReadFromUDP(rxBuf)
		if err != nil {
			if time.Since(idleStart) > idleTimeout {
				fmt.Println("[udp] idle timeout")
				return nil
			}
			continue
		}
		idleStart = time.Now()
		if n < 22 { // header min
			continue
		}

		// Check xferId
		if !bytes.Equal(rxBuf[:16], idBytes) {
			continue
		}

		seq := int(binary.BigEndian.Uint32(rxBuf[16:20])) // 0-based
		size := int(binary.BigEndian.Uint16(rxBuf[20:22]))
		if size < 0 || size > maxPayload || 22+size > n {
			continue
		}
		if seq < 0 || seq >= expected {
			continue
		}

		offset := seq * maxPayload
		end := offset + size
		if end > len(fileBytes) || offset < 0 {
			continue
		}

		if !received[seq] {
			copy(fileBytes[offset:end], rxBuf[22:22+size])
			received[seq] = true
			receivedCount++
		}

		// Unified ACK
		if _, err := ackW.WriteString(fmt.Sprintf("ACK_UDP:%s:%d\n", strings.ToLower(metadata.XferId), seq)); err == nil {
			_ = ackW.Flush()
		}

		if receivedCount >= expected {
			break
		}
	}

	// Final SHA-256 check
	sum := sha256.Sum256(fileBytes)
	got := strings.ToLower(hex.EncodeToString(sum[:]))
	want := strings.ToLower(metadata.Hash)
	if got != want {
		fmt.Printf("[logs] hash mismatch: got=%s want=%s\n", got, want)
		_, _ = conn.Write([]byte("error:hash_mismatch\n"))
		return nil
	}
	return fileBytes
}

func StartUDPReceiver(port int, metadata config.FileMetadata, conn net.Conn, done chan bool) []byte {
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		fmt.Println("[!] resolve UDP addr:", err)
		select { case done <- true: default: }
		return nil
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("[!] listen UDP:", err)
		select { case done <- true: default: }
		return nil
	}
	defer udpConn.Close()

	// Bigger socket buffer helps at higher rates
	_ = udpConn.SetReadBuffer(4 << 20) // 4 MiB

	// Drive the connection-level handler and wait for it to signal completion
	doneStruct := make(chan struct{}, 1)
	data := StartUDPReceiverConn(udpConn, metadata, conn, doneStruct)

	// Always propagate completion to the caller's 'done' channel
	<-doneStruct
	select { case done <- true: default: }

	return data
}