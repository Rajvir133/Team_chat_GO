package transfer

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"go_files/config"
)




// SendMessage sends either a file or text message
func SendMessage(msg config.Message, tcpPort int) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", msg.Receiver, tcpPort))
	if err != nil {
		return fmt.Errorf("TCP connect failed: %v", err)
	}
	defer conn.Close()

	bufWriter := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	if msg.MessageType == "image" || msg.MessageType == "video" {
		for _, file := range msg.Payload {
			rawBytes, err := config.DecodeBase64(file.Data)
			if err != nil {
				return fmt.Errorf("base64 decode error: %v", err)
			}

			var compressedData []byte
			if config.NoCompressionTypes[file.Type] || len(rawBytes) < 1024 {
				compressedData = rawBytes
			} else {
				var compressed bytes.Buffer
				zlibWriter := zlib.NewWriter(&compressed)
				if _, err = zlibWriter.Write(rawBytes); err != nil {
					return fmt.Errorf("compression error: %v", err)
				}
				zlibWriter.Close()
				compressedData = compressed.Bytes()
			}

			hash := sha256.Sum256(compressedData)
			chunks := config.CalculateChunks(len(compressedData))
			metadata := config.FileMetadata{
				Name:     file.Name,
				Type:     file.Type,
				Size:     len(compressedData),
				Chunks:   chunks,
				Hash:     fmt.Sprintf("%x", hash[:]),
				Sender:   msg.Sender,
				Receiver: msg.Receiver,
			}

			metaBytes, _ := json.Marshal(metadata)
			bufWriter.Write(append(metaBytes, '\n'))
			bufWriter.Flush()

			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read start response: %v", err)
			}
			response = strings.TrimSpace(response)
			if strings.HasPrefix(response, "ERROR:") {
				return fmt.Errorf("receiver error: %s", response)
			}

			var udpPort int
			if _, err := fmt.Sscanf(response, "Start:%d", &udpPort); err != nil {
				return fmt.Errorf("invalid start response: %q", response)
			}

			if err := SendUDPChunks(conn, metadata, compressedData); err != nil {
				return err
			}
		}
		return nil
	}

	jsonData, _ := json.Marshal(msg)
	_, err = conn.Write(append(jsonData, '\n'))
	return err
}
