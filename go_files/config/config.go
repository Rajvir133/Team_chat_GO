package config

import (
	"encoding/base64"
	"os"
)

// ======== Configuration constants ========
const (
	HTTPPort     = 8080
	TCPPort      = 9200
	ChunkSize    = 32768
	AckTimeoutMs = 10000
	MaxRetries   = 1
	IPBase       = "192.168.29."
	FastAPIHost  = "127.0.0.1"
	FastAPIPort  = 5000
)

// ======== File type handling ========
var NoCompressionTypes = map[string]bool{
	"image/jpeg": true,
	"image/jpg":  true,
	"image/png":  true,
	"image/gif":  true,
	"video/mp4":  true,
	"video/avi":  true,
	"video/mov":  true,
}

// ======== Protocol Structures ========

// Message represents a chat or file message
type Message struct {
	Sender      string        `json:"sender"`
	Receiver    string        `json:"receiver"`
	MessageType string        `json:"message_type"`
	Message     string        `json:"message"`
	Payload     []FilePayload `json:"payload"`
}

// FilePayload contains file name, type, and base64 data
type FilePayload struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
}

// FileMetadata contains file details before sending
type FileMetadata struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     int    `json:"size"`
	Chunks   int    `json:"chunks"`
	Hash     string `json:"hash"`
	Sender   string `json:"sender"`
	Receiver string `json:"receiver"`
	Message  string `json:"message"`
}

// ======== Base64 helpers ========
func DecodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// EncodeBase64FromFile reads a file and encodes it to base64
func EncodeBase64FromFile(filePath string) string {
	
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return EncodeBase64(data)
}

// ======== Utility ========
func CalculateChunks(fileSize int) int {
	chunks := fileSize / ChunkSize
	if fileSize%ChunkSize != 0 {
		chunks++
	}
	return chunks
}
