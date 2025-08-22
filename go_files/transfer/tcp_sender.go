package transfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"bytes"
	"compress/zlib"
	"go_files/config"
)

func Send_TCP(msg config.Message) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", msg.Receiver, config.TCPPort))
	if err != nil {
		return fmt.Errorf("[logs] TCP connect error: %v", err)
	}
	defer conn.Close()

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
	rawBytes, _ := config.DecodeBase64(file.Data)

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

	metaBytes, _ := json.Marshal(metadata)
	writer.Write(append(metaBytes, '\n'))
	writer.Flush()

	startLine, _ := reader.ReadString('\n')
	if len(startLine) == 0 {
		return fmt.Errorf("[logs] no Start signal from receiver")
	}
	var udpPort int
	fmt.Sscanf(startLine, "Start:%d\n", &udpPort)
	fmt.Printf("[TCP] received start : %d\n", udpPort)

	err = SendFileChunksUDP(conn,msg.Sender, msg.Receiver, udpPort, hash, compressedData, chunks)
	if err != nil {
		return err
	}

	stopLine, _ := reader.ReadString('\n')
	if stopLine == "stop\n" {
		fmt.Println("[TCP] received : stop")
		fmt.Println("[log] sent successfully")
	}
	return nil
}
