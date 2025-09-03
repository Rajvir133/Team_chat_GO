package transfer

import (
	"bufio"
	"crypto/sha256"
	"time"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"bytes"
	"compress/zlib"
	"go_files/config"
)

func Send_TCP(msg config.Message) error {
	d := net.Dialer{KeepAlive: 30 * time.Second, Timeout: 10 * time.Second}
	conn, err := d.Dial("tcp", fmt.Sprintf("%s:%d", msg.Receiver, config.TCPPort))
	if err != nil {
		return fmt.Errorf("[logs] TCP connect error: %v", err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	if msg.MessageType == "text" {
		if err := json.NewEncoder(conn).Encode(msg); err != nil {
			return fmt.Errorf("failed to send text message: %v", err)
		}
		fmt.Println("[logs] text message sent successfully")
		return nil
	}

	if len(msg.Payload) == 0 {
		return fmt.Errorf("[logs] file transfer requested but payload is empty")
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	file := msg.Payload[0]
	rawBytes := file.Data

	var compressedData []byte
	if config.NoCompressionTypes[file.Type] {
		compressedData = rawBytes
	} else {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		w.Write(rawBytes)
		w.Close()
		compressedData = buf.Bytes()
	}

	hash := sha256.Sum256(compressedData)
	hashHex := fmt.Sprintf("%x", hash[:])
	chunks := config.CalculateChunks(len(compressedData))

	metadata := config.FileMetadata{
		Name:     file.Name,
		Type:     file.Type,
		Size:     len(compressedData),
		Chunks:   chunks,
		Hash:     hashHex,
		Sender:   msg.Sender,
		Receiver: msg.Receiver,
		Message:  msg.Message,
	}

	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %v", err)
	}
	if _, err := writer.Write(append(metaBytes, '\n')); err != nil {
		return fmt.Errorf("write metadata: %v", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush metadata: %v", err)
}

	startLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("[logs] failed to read Start line: %v", err)
	}
	if !strings.HasPrefix(startLine, "Start:") {
		return fmt.Errorf("[logs] invalid Start line: %q", startLine)
	}
	var udpPort int
	if _, err := fmt.Sscanf(startLine, "Start:%d\n", &udpPort); err != nil {
		return fmt.Errorf("[logs] could not parse UDP port from %q: %v", startLine, err)
	}
	fmt.Printf("[TCP] received start at %d\n", udpPort)

	err = SendFileChunksUDP(conn,msg.Sender, msg.Receiver, udpPort, hash, compressedData, chunks)
	if err != nil {
		return err
	}

	stopLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read final line: %v", err)
	}
	switch stopLine {
	case "stop\n":
		fmt.Println("[TCP] received stop")
		fmt.Println("[log] sent successfully")
	case "error:timeout\n":
		return fmt.Errorf("receiver timeout waiting for chunks")
	case "error:hash_mismatch\n":
		return fmt.Errorf("receiver hash mismatch")
	case "error:udp_read\n":
		return fmt.Errorf("receiver experienced UDP read error")
	case "error:receive_failed\n":
		return fmt.Errorf("receiver failed during receive")
	default:
		return fmt.Errorf("unexpected final line: %q", stopLine)
	}
	return nil
}