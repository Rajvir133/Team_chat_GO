package transfer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go_files/config"
)

func SendFileChunksUDP(connTCP net.Conn, receiverIP string, udpPort int, fileHash [32]byte, data []byte, totalChunks int) error {
	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", receiverIP, udpPort))
	udpConn, _ := net.DialUDP("udp", nil, udpAddr)
	defer udpConn.Close()

	reader := bufio.NewReader(connTCP)

	for i := 1; i <= totalChunks; i++ {
		start := (i - 1) * config.ChunkSize
		end := start + config.ChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunkData := data[start:end]

		var header bytes.Buffer
		header.Write(fileHash[:])
		binary.Write(&header, binary.BigEndian, uint32(i))
		binary.Write(&header, binary.BigEndian, uint32(totalChunks))
		binary.Write(&header, binary.BigEndian, uint32(len(chunkData)))

		packet := append(header.Bytes(), chunkData...)

		// Send-with-ACK loop: do not proceed to next chunk until proper ACK
		ackReceived := false
		maxRetries := 1
		for attempt := 0; attempt <= maxRetries; attempt++ {
			_, err := udpConn.Write(packet)
			if err != nil {
				return fmt.Errorf("failed to send UDP chunk %d: %v", i, err)
			}
			// fmt.Printf("[UDP] send chunk %d\n", i)

			connTCP.SetReadDeadline(time.Now().Add(10 * time.Second))
			ack, err := reader.ReadString('\n')
			if err == nil && ack == fmt.Sprintf("chunk%d\n", i) {
				// fmt.Printf("[UDP] ack of chunk %d received\n", i)
				ackReceived = true
				break
			}
			if err != nil {
				fmt.Printf("[logs] ack wait error for chunk %d (attempt %d): %v\n", i, attempt+1, err)
			} else {
				fmt.Printf("[logs] unexpected ack for chunk %d (attempt %d): %q\n", i, attempt+1, ack)
			}
			// retry will re-send the same chunk
		}
		if !ackReceived {
			return fmt.Errorf("[log]no ACK for chunk %d after retries", i)
		}
	}
	return nil
}
