package transfer

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"time"

	"go_files/config"
)

func StartUDPReceiver(port int, metadata config.FileMetadata, filePath string, conn net.Conn, done chan bool) {
	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	udpConn, _ := net.ListenUDP("udp", udpAddr)
	defer udpConn.Close()

	file, _ := os.Create(filePath)
	defer file.Close()

	received := make(map[int]bool)

	for len(received) < metadata.Chunks {
		buf := make([]byte, config.ChunkSize+44)
		udpConn.SetReadDeadline(time.Now().Add(20 * time.Second))
		n, _, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("[!] UDP read error:", err)
			break
		}

		// Header: 32 bytes hash, 4 bytes index, 4 bytes total, 4 bytes size
		if n < 44 {
			continue
		}
		idx := int(uint32(buf[32])<<24 | uint32(buf[33])<<16 | uint32(buf[34])<<8 | uint32(buf[35]))
		size := int(uint32(buf[40])<<24 | uint32(buf[41])<<16 | uint32(buf[42])<<8 | uint32(buf[43]))
		data := buf[44:n]

		if !received[idx] {
			offset := int64((idx - 1) * config.ChunkSize)
			file.WriteAt(data[:size], offset)
			received[idx] = true
			fmt.Printf("[TCP] chunk %d received\n", idx)
		}
		// Always ACK the received index so sender can progress
		conn.Write([]byte(fmt.Sprintf("chunk%d\n", idx)))
	}

	file.Sync()

	// verify hash
	file.Seek(0, 0)
	hasher := sha256.New()
	fi, _ := os.Stat(filePath)
	data := make([]byte, fi.Size())
	file.Read(data)
	hasher.Write(data)
	if fmt.Sprintf("%x", hasher.Sum(nil)) == metadata.Hash {
		done <- true
	} else {
		fmt.Println("[❌] hash mismatch")
	}
}
