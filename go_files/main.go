package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"strings"

	"go_files/config"
	"go_files/transfer"
)

// Thread-safe pools
var connectionPool sync.Map   // outgoing: key=ip
var dialLocks      sync.Map   // per-IP *sync.Mutex
var sendLocks      sync.Map   // per-IP *sync.Mutex

func getSendLock(ip string) *sync.Mutex {
	if v, ok := sendLocks.Load(ip); ok { return v.(*sync.Mutex) }
	m := &sync.Mutex{}
	if actual, loaded := sendLocks.LoadOrStore(ip, m); loaded { return actual.(*sync.Mutex) }
	return m
}
func getDialLock(ip string) *sync.Mutex {
	if v, ok := dialLocks.Load(ip); ok { return v.(*sync.Mutex) }
	m := &sync.Mutex{}
	if actual, loaded := dialLocks.LoadOrStore(ip, m); loaded { return actual.(*sync.Mutex) }
	return m
}

// Returns any live conn (outgoing takes priority, otherwise incoming)
func getAnyConn(ip string) (net.Conn, bool) {
	if v, ok := connectionPool.Load(ip); ok {
		if c, ok2 := v.(net.Conn); ok2 { return c, true }
	}
	if v, ok := connectionPool.Load(ip + "|in"); ok {
		if c, ok2 := v.(net.Conn); ok2 { return c, true }
	}
	return nil, false
}

func main() {
	// 🔗 wire identity -> registry
	transfer.OnIdentity = func(remoteIP, hostname, tag string) {
    rememberPeerWithTag(remoteIP, hostname, tag)
    if strings.EqualFold(tag, "CMK") {
        markAndroidPeer(remoteIP, true)
    }
}

	transfer.SetOnConnReady(func(ip string, conn net.Conn) {
		storeIncomingConn(ip, conn)
	})
	transfer.SetOnConnClosed(func(ip string, conn net.Conn) {
		if v, ok := connectionPool.Load(ip); ok && v == conn {
			connectionPool.Delete(ip)
			log.Printf("[pool] removed OUTGOING conn for %s\n", ip)
		}
		if v, ok := connectionPool.Load(ip + "|in"); ok && v == conn {
			connectionPool.Delete(ip + "|in")
			log.Printf("[pool] removed INCOMING conn for %s\n", ip)
		}
	})

	// Start TCP server
	go transfer.StartTCPServer(config.TCPPort)

	// HTTP routes
	http.HandleFunc("/send", SendHandler)
	http.HandleFunc("/scan", ScanHandler)

	log.Printf("🌐 HTTP server running on :%d\n", config.HTTPPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.HTTPPort), nil); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

// --- Storage with "first wins" policy ---
func storeIncomingConn(ip string, conn net.Conn) {
	key := ip + "|in"
	if old, loaded := connectionPool.LoadAndDelete(key); loaded {
		if oc, ok := old.(net.Conn); ok && oc != conn { _ = oc.Close() }
	}
	connectionPool.Store(key, conn)
	log.Printf("[pool] stored INCOMING conn for %s (key=%s)\n", ip, key)
}
func storeOutgoingConn(ip string, conn net.Conn) {
	if old, loaded := connectionPool.LoadAndDelete(ip); loaded {
		if oc, ok := old.(net.Conn); ok && oc != conn { _ = oc.Close() }
	}
	connectionPool.Store(ip, conn)
	log.Printf("[pool] stored OUTGOING conn for %s (key=%s)\n", ip, ip)
}
