package config

// ======== Configuration constants ========
const (
	HTTPPort     = 8080
	TCPPort      = 9200
	ChunkSize    = 22768      // change this size from 32 kb to 12kb
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
	Data []byte `json:"byte"`
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

// ======== Utility ========
func CalculateChunks(fileSize int) int {
	chunks := fileSize / ChunkSize
	if fileSize%ChunkSize != 0 {
		chunks++
	}
	return chunks
}
