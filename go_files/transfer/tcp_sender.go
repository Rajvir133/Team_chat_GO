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
	"time"
	"path/filepath"

	"go_files/config"
)

func Send_TCP(msg config.Message, conn net.Conn) error {
	// Prefer low-latency control channel
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// ---------- TEXT (pipe format): sender|receiver|text|message ----------
	if strings.EqualFold(msg.MessageType, "TEXT") {
		line := pipeEscape(msg.Sender) + "|" +
			pipeEscape(msg.Receiver) + "|" +
			pipeEscape(msg.Message) + "|" +
			"TEXT" + "|" +
			config.Device_type + "\n"

		if _, err := writer.WriteString(line); err != nil {
			return fmt.Errorf("failed to write text line: %v", err)
		}

		fmt.Println(strings.Repeat("-", 50))
		fmt.Println(strings.Repeat(" ", 50))
		fmt.Printf("Metadata Payload  %s\n", line)
		fmt.Println(strings.Repeat(" ", 50))
		fmt.Println(strings.Repeat("-", 50))

		if err := writer.Flush(); err != nil {
			return fmt.Errorf("failed to flush text line: %v", err)
		}
		fmt.Println("[logs] text message sent successfully (pipe)")
		return nil
	}

	// ---------- FILE (pipe metadata + UDP data) ----------
	if len(msg.Payload) == 0 {
		return fmt.Errorf("[logs] file transfer requested but payload is empty")
	}
	file := msg.Payload[0]
	rawBytes := file.Data

	// Compression (skipped for certain types)
	var compressed []byte
	if config.NoCompressionTypes[file.Type] {
		compressed = rawBytes
	} else {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		_, _ = w.Write(rawBytes)
		_ = w.Close()
		compressed = buf.Bytes()
	}

	hash := sha256.Sum256(compressed)
	hashHex := fmt.Sprintf("%x", hash[:])
	chunks := config.CalculateChunks(len(compressed))
	fileExt := strings.TrimPrefix(filepath.Ext(file.Name), ".")

	// NEW: JSON header
	type tcpMeta struct {
		Sender       string `json:"sender"`
		Receiver     string `json:"receiver"`
		FileName     string `json:"file_name"`
		FileType     string `json:"file_type"`
		FileExt      string `json:"file_ext"`
		FileSize     int    `json:"file_size"`
		FileChecksum string `json:"file_checksum"`
		TotalChunks  int    `json:"total_chunks"`
		Host         string `json:"host"`
		Transport    string `json:"transport"`
	}
	hdr := tcpMeta{
		Sender: msg.Sender, Receiver: msg.Receiver,
		FileName: file.Name, FileType: file.Type, FileExt: fileExt,
		FileSize: len(compressed), FileChecksum: hashHex, TotalChunks: chunks,
		Host: config.Device_type, Transport: "UDP",
	}
	b, _ := json.Marshal(hdr)
	metaLine := string(b) + "\n"

	if _, err := writer.WriteString(metaLine); err != nil {
		return fmt.Errorf("write metadata (json): %v", err)
	}
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println(strings.Repeat(" ", 50))
	fmt.Printf("Metadata JSON  %s\n", metaLine)
	fmt.Println(strings.Repeat(" ", 50))
	fmt.Println(strings.Repeat("-", 50))

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush metadata: %v", err)
	}

	// --- Wait for Start:<port>, skipping identity/heartbeat noise
	var startLine string
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("[logs] failed to read Start line: %v", err)
		}
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "START_UDP:") {
			startLine = line
			break
		}

		// Identity (tag|host) while we wait? Record & ignore.
		if i := strings.Index(line, "|"); i > 0 {
			tag := strings.TrimSpace(line[:i])
			host := strings.TrimSpace(line[i+1:])
			if tag == "BMS" || tag == "CMK" {
				if OnIdentity != nil {
					peerHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
					if peerHost == "" {
						peerHost = conn.RemoteAddr().String()
					}
					OnIdentity(peerHost, host)
				}
				fmt.Printf("[handshake] ignoring identity line: %q\n", line)
				continue
			}
		}

		// Heartbeats
		if line == "ALIVE" || line == "PONG" {
			continue
		}

		return fmt.Errorf("[logs] invalid Start line: %q", line)
	}
	_ = conn.SetReadDeadline(time.Time{})

	// Parse UDP port
	var udpPort int
	if _, err := fmt.Sscanf(startLine, "START_UDP:%d", &udpPort); err != nil {
		return fmt.Errorf("[logs] could not parse UDP port from %q: %v", startLine, err)
	}
	fmt.Printf("[TCP] received start at %d\n", udpPort)

	// UDP target = actual TCP peer IP (never rely on hostname here)
	peerIP := func() string {
		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			return conn.RemoteAddr().String()
		}
		return host
	}()

	if err := SendFileChunksUDP(conn, msg.Sender, peerIP, udpPort, hash, compressed, chunks); err != nil {
		return err
	}

	// --- Final status
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	stopLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read final line: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})

	switch strings.TrimSpace(stopLine) {
	case "STOP_UDP":
		fmt.Println("[TCP] received stop")
		fmt.Println("[log] sent successfully")
	case "error:timeout":
		return fmt.Errorf("receiver timeout waiting for chunks")
	case "error:hash_mismatch":
		return fmt.Errorf("receiver hash mismatch")
	case "error:udp_read":
		return fmt.Errorf("receiver experienced UDP read error")
	case "error:receive_failed":
		return fmt.Errorf("receiver failed during receive")
	default:
		return fmt.Errorf("unexpected final line: %q", stopLine)
	}
	return nil
}

// We keep this JSON helper import to avoid go vet complaining about unused import
// when you later remove JSON entirely. If you fully move to pipe-only, you can
// delete the json import above.
var _ = json.Marshal

// ----- helpers -----

func pipeEscape(s string) string {
	// escape backslash first, then special chars
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `|`, `\|`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}
