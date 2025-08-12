package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"go_files/config"
)

func StartTCPServer(port int) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	fmt.Printf("[ TCP ] server listening on port %d...\n", port)

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

	var metadata config.FileMetadata
	if err := json.Unmarshal([]byte(metaLine), &metadata); err != nil {
		fmt.Println("[!] Invalid metadata:", err)
		return
	}

	fmt.Printf("Incoming file : %s , %d , %d,%s\n",
		metadata.Name, metadata.Size, metadata.Chunks, metadata.Sender)

	udpPort := allocateUDPPort()
	fmt.Printf("[TCP] start : %d\n", udpPort)
	conn.Write([]byte(fmt.Sprintf("Start:%d\n", udpPort)))

	saveDir := "received_media"
	os.MkdirAll(saveDir, 0755)
	filePath := filepath.Join(saveDir, metadata.Name)

	done := make(chan bool)
	go StartUDPReceiver(udpPort, metadata, filePath, conn, done)

	select {
	case <-done:
		fmt.Println("[logs] all chunk received")
		fmt.Println("[logs] verified")
		conn.Write([]byte("stop\n"))
		fmt.Println("[logs] sending stop")
	case <-time.After(120 * time.Second):
		fmt.Println("[logs] Timeout waiting for file")
	}
}

func allocateUDPPort() int {
	l, _ := net.ListenPacket("udp", ":0")
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}
