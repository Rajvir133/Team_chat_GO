package transfer

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go_files/config"
)

func StartUDPReceiver(port int, metadata config.FileMetadata, conn net.Conn, done chan bool) []byte {
    udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
    udpConn, _ := net.ListenUDP("udp", udpAddr)
    defer udpConn.Close()

    // 🆕 ONLY store in memory during transfer
    fileBytes := make([]byte, metadata.Size)
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

        idx := int(binary.BigEndian.Uint32(buf[32:36]))
        size := int(binary.BigEndian.Uint32(buf[40:44]))
        data := buf[44:n]

        if !received[idx] {
            offset := (idx - 1) * config.ChunkSize
            copy(fileBytes[offset:], data[:size])
            received[idx] = true
            // fmt.Printf("[UDP] chunk %d received\n", idx)
        }

        // Always ACK the received index so sender can progress
        conn.Write([]byte(fmt.Sprintf("chunk%d\n", idx)))
    }

    hasher := sha256.New()
    hasher.Write(fileBytes)
    
    if fmt.Sprintf("%x", hasher.Sum(nil)) == metadata.Hash {
        fmt.Println("[logs] Hash verification successful")
        
        done <- true
        return fileBytes
    } else {
        fmt.Println("[logs] hash mismatch")
        return nil
    }
}