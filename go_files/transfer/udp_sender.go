package transfer

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"go_files/config"
)


func SendFileChunksUDP(connTCP net.Conn, sender, receiverIP string, udpPort int, fileHash [32]byte, data []byte, chunks int, xferId string) error {
	addr := fmt.Sprintf("%s:%d", receiverIP, udpPort)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil { return fmt.Errorf("resolve udp addr: %w", err) }
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil { return fmt.Errorf("dial udp: %w", err) }
	defer udpConn.Close()
	_ = udpConn.SetWriteBuffer(4 << 20)

	idBytes, err := hex.DecodeString(strings.ToLower(xferId))
	if err != nil || len(idBytes) != 16 {
		return fmt.Errorf("invalid xferId %q", xferId)
	}

	reader := bufio.NewReader(connTCP)
	chunkSize := config.ChunkSize

	for i := 0; i < chunks; i++ {
		off := i * chunkSize
		n := chunkSize
		if off+n > len(data) { n = len(data) - off }
		if n < 0 { n = 0 }
		payload := data[off : off+n]

		// [16B xferId] [u32 seq(0-based)] [u16 len] [payload]
		header := make([]byte, 16+4+2)
		copy(header[0:16], idBytes)
		binary.BigEndian.PutUint32(header[16:20], uint32(i))
		binary.BigEndian.PutUint16(header[20:22], uint16(len(payload)))
		packet := append(header, payload...)

		ackTarget := fmt.Sprintf("ACK_UDP:%s:%d", strings.ToLower(xferId), i)
		ackReceived := false

		for attempt := 0; attempt <= config.MaxRetries; attempt++ {
			_ = udpConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := udpConn.Write(packet); err != nil {
				if attempt == config.MaxRetries {
					return fmt.Errorf("[udp] send chunk %d failed: %w", i, err)
				}
				time.Sleep(time.Duration(250*(1<<attempt)) * time.Millisecond)
				continue
			}

			_ = connTCP.SetReadDeadline(time.Now().Add(time.Duration(config.AckTimeoutMs) * time.Millisecond))
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if attempt == config.MaxRetries {
						return fmt.Errorf("[udp] ack error for chunk %d: %w", i, err)
					}
					break // resend this chunk
				}
				l := strings.TrimSpace(line)
				if l == "" { continue }
				if l == "KEEP_ALIVE" || l == "ALIVE" || l == "PING" || l == "PONG" {
					continue
				}
				if l == ackTarget {
					ackReceived = true
					break
				}
				// ignore other lines
			}
			if ackReceived { break }
			time.Sleep(time.Duration(250*(1<<attempt)) * time.Millisecond)
		}
		if !ackReceived {
			return fmt.Errorf("[udp] no ACK for chunk %d from %s", i, receiverIP)
		}
	}
	return nil
}