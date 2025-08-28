package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
	"go_files/config"
    "net/http" 	
	"bytes" 
)

func StartTCPServer(port int) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	fmt.Printf("[TCP] server listening on port %d...\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("[!] TCP accept error:", err)
			continue
		}
		go handleTCPConnection(conn)
	}
}




func handleTCPConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	metaLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("[!] Failed to read metadata:", err)
		return
	}


	var Message config.Message
	if err := json.Unmarshal([]byte(metaLine), &Message); err == nil {
		fmt.Printf("message_type : %s\n", Message.MessageType)


	if Message.MessageType == "text"{

		metadata := config.FileMetadata{
            Sender:   Message.Sender,
            Receiver: Message.Receiver,
            Type:     Message.MessageType,
            Message:  Message.Message,
            Name:     "",
            Size:     0,
            Chunks:   0,
            Hash:     "",
		}

		go notifyFastAPI(metadata, []byte{})
		fmt.Println("[logs] Text message sent to FastAPI")
		return
	}

	var metadata config.FileMetadata
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		fmt.Println("[!] Invalid metadata:", err)
		return
	}

	fmt.Printf("[logs] Incoming file : %s , %d , %d,%s , %s\n",
		metadata.Name, metadata.Size, metadata.Chunks, metadata.Sender,metadata.Type)
	
	udpPort := allocateUDPPort()
	fmt.Printf("[TCP] start : %d\n", udpPort)
	conn.Write([]byte(fmt.Sprintf("Start:%d\n", udpPort)))
	
	fileDataChan := make(chan []byte)
	done := make(chan bool)

    go func() {
        fileBytes := StartUDPReceiver(udpPort, metadata, conn, done)
        fileDataChan <- fileBytes
    }()

	select {
	case <-done:
		combinedFileData := <-fileDataChan

		fmt.Println("[logs] all chunk received")
		conn.Write([]byte("stop\n"))
		fmt.Println("[TCP] sending stop")
		go notifyFastAPI(metadata, combinedFileData)
	case <-time.After(120 * time.Second):
		fmt.Println("[logs] Timeout waiting for file")
	}
  }
}

func allocateUDPPort() int {
	l, _ := net.ListenPacket("udp", ":0")
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func notifyFastAPI(metadata config.FileMetadata, combinedFileData []byte) error {
    base64Data := config.EncodeBase64(combinedFileData)

    msg := config.Message{
        Sender:      metadata.Sender,
        Receiver:    metadata.Receiver,
        MessageType: metadata.Type,
        Message:   metadata.Message,
    	Payload: []config.FilePayload{      
        {                                 
            Name: metadata.Name,
            Type: metadata.Type,
            Data: base64Data,               
        },                                
    },
	}
	
    body, err := json.Marshal(msg)
    if err != nil {
		fmt.Printf("JSON marshal error: %v\n", err)
        return fmt.Errorf("json marshal error: %w", err)
    }

    url := fmt.Sprintf("http://%s:%d/go_message", config.FastAPIHost, config.FastAPIPort)
    resp, err := http.Post(url, "application/json", bytes.NewReader(body))
    if err != nil {
		fmt.Printf("HTTP post error: %v\n", err)
        return fmt.Errorf("HTTP post error: %w", err)
    }
    resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
		fmt.Printf("FastAPI returned status %d\n", resp.StatusCode)
        return fmt.Errorf("FastAPI returned status %d", resp.StatusCode)
    }
    fmt.Printf("[✓] Notified FastAPI about %s (%d bytes)\n", metadata.Name, len(combinedFileData))
    return nil
}