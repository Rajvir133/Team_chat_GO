package transfer

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"crypto/rand"
    "encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"go_files/config"
)

func Send_TCP(msg config.Message, conn net.Conn) error {
	// Prefer low-latency control channel
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// ---------- TEXT (pipe format): sender|receiver|text|TEXT|<DeviceTag> ----------
	if strings.EqualFold(msg.MessageType, "TEXT") {
		line := pipeEscape(msg.Sender) + "|" +
			pipeEscape(msg.Receiver) + "|" +
			pipeEscape(msg.Message) + "|" +
			"TEXT" + "|" +
			config.Device_type + "\n"

		if _, err := writer.WriteString(line); err != nil {
			return fmt.Errorf("failed to write text line: %v", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("failed to flush text line: %v", err)
		}
		fmt.Println("[logs] text message sent successfully (pipe)")
		return nil
	}

	// ---------- FILE (UNIFIED: JSON header over TCP + UDP datagrams + END_UDP) ----------
	if len(msg.Payload) == 0 {
		return fmt.Errorf("[logs] file transfer requested but payload is empty")
	}
	file := msg.Payload[0]
	rawBytes := file.Data

	// Compression (skip for certain MIME types)
	var compressed []byte
	if config.NoCompressionTypes[file.Type] {
		compressed = rawBytes
	} else {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(rawBytes); err != nil {
			return fmt.Errorf("zlib write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("zlib close: %w", err)
		}
		compressed = buf.Bytes()
	}

	hash := sha256.Sum256(compressed)
	hashHex := fmt.Sprintf("%x", hash[:])
	chunks := config.CalculateChunks(len(compressed))
	fileExt := strings.TrimPrefix(filepath.Ext(file.Name), ".")

	// Unified JSON header
	type unifiedMeta struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Ext       string `json:"ext"`
		Size      int    `json:"size"`
		Checksum  string `json:"checksum"`
		Transport string `json:"transport"`
		XferId    string `json:"xferId"`
		Marker    string `json:"marker"`
	}
	hdr := unifiedMeta{
		From:      msg.Sender,
		To:        msg.Receiver,
		Type:      file.Type,
		Name:      file.Name,
		Ext:       fileExt,
		Size:      len(compressed),
		Checksum:  hashHex,            // sha256 of EXACT bytes sent
		Transport: "UDP",
		XferId:    generateXferID(),   // 32-hex
		Marker:    config.Device_type, // "BMS" etc.
	}
	b, _ := json.Marshal(hdr)
	metaLine := string(b) + "\n"

	if _, err := writer.WriteString(metaLine); err != nil {
		return fmt.Errorf("write metadata (json): %v", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush metadata: %v", err)
	}

	// --- Wait for START_UDP:<port>[:<xferId>], skipping identity + heartbeats
	var startLine string
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("[logs] failed to read START_UDP line: %v", err)
		}
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "START_UDP:") {
			startLine = line
			break
		}

		// Identity (tag|host): accept but ignore while waiting
		if i := strings.Index(line, "|"); i > 0 {
			tag := strings.TrimSpace(line[:i])
			host := strings.TrimSpace(line[i+1:])
			if tag == "BMS" || tag == "CMK" {
				if OnIdentity != nil {
					peerHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
					if peerHost == "" {
						peerHost = conn.RemoteAddr().String()
					}
					OnIdentity(peerHost, host, tag)
				}
				continue
			}
		}

		// Heartbeats/noise
		if line == "KEEP_ALIVE" || line == "ALIVE" || line == "PING" || line == "PONG" {
			continue
		}

		return fmt.Errorf("[logs] invalid line while waiting START_UDP: %q", line)
	}
	_ = conn.SetReadDeadline(time.Time{})

	// Parse UDP port and echoed xferId (if provided)
	var udpPort int
	var echoedXfer string
	parts := strings.Split(startLine, ":")
	if len(parts) < 2 || parts[0] != "START_UDP" {
		return fmt.Errorf("[logs] malformed START_UDP: %q", startLine)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &udpPort); err != nil {
		return fmt.Errorf("[logs] bad UDP port in %q: %v", startLine, err)
	}
	if len(parts) >= 3 {
		echoedXfer = strings.ToLower(parts[2])
	}
	fmt.Printf("[TCP] received start at %d (xferId=%s)\n", udpPort, echoedXfer)

	// UDP target = actual TCP peer IP
	peerIP := func() string {
		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			return conn.RemoteAddr().String()
		}
		return host
	}()

	// Choose xferId to use (prefer echoed)
	xfer := strings.ToLower(hdr.XferId)
	if echoedXfer != "" {
		xfer = echoedXfer
	}

	// Stream chunks (unified layout; ACKs read inside)
	if err := SendFileChunksUDP(conn, msg.Sender, peerIP, udpPort, hash, compressed, chunks, xfer); err != nil {
		return err
	}

	// Unified finish: sender emits END_UDP:<xferId>; receiver doesn't send STOP_UDP
	if _, err := writer.WriteString(fmt.Sprintf("END_UDP:%s\n", xfer)); err == nil {
		_ = writer.Flush()
	}

	// Optional: brief window to catch error lines; otherwise success.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, err := reader.ReadString('\n'); err == nil {
		l := strings.TrimSpace(line)
		switch l {
		case "error:timeout":
			return fmt.Errorf("receiver timeout waiting for chunks")
		case "error:hash_mismatch":
			return fmt.Errorf("receiver hash mismatch")
		case "error:udp_read", "error:receive_failed":
			return fmt.Errorf(l)
		default:
			// ignore any other noise
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	fmt.Println("[log] sent successfully")
	return nil
}

// Keep JSON helper to quiet vet if you later drop JSON entirely.
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


func generateXferID() string {
    b := make([]byte, 16) // 16 bytes -> 32 hex chars
    _, _ = rand.Read(b)
    return hex.EncodeToString(b)
}