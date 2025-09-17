package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"go_files/config"
	"go_files/transfer"
)

// Thread-safe pool (handlers call Load/Store/Delete on this)
var connectionPool sync.Map

func main() {

	transfer.SetOnConnReady(func(ip string, conn net.Conn) {
    	storeIncomingConn(ip, conn)
	})

	transfer.SetOnConnClosed(func(ip string, conn net.Conn) {
		
		if v, ok := connectionPool.Load(ip); ok {
			if v == conn {
				connectionPool.Delete(ip)
				log.Printf("[pool] removed OUTGOING conn for %s\n", ip)
			}
		}
		if v, ok := connectionPool.Load(ip + "|in"); ok {
			if v == conn {
				connectionPool.Delete(ip + "|in")
				log.Printf("[pool] removed INCOMING conn for %s\n", ip)
			}
		}
	})

	// 2) Start TCP server AFTER the hook is registered
	go transfer.StartTCPServer(config.TCPPort)

	// 3) HTTP routes
	http.HandleFunc("/send", SendHandler)
	http.HandleFunc("/scan", ScanHandler)

	log.Printf("🌐 HTTP server running on :%d\n", config.HTTPPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.HTTPPort), nil); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}










func storeIncomingConn(ip string, conn net.Conn) {
    key := ip + "|in"
    if old, loaded := connectionPool.LoadAndDelete(key); loaded {
        if oc, ok := old.(net.Conn); ok && oc != conn {
            _ = oc.Close()
        }
    }
    connectionPool.Store(key, conn)
    log.Printf("[pool] stored INCOMING conn for %s (key=%s)\n", ip, key)
}

func storeOutgoingConn(ip string, conn net.Conn) {
    if old, loaded := connectionPool.LoadAndDelete(ip); loaded {
        if oc, ok := old.(net.Conn); ok && oc != conn {
            _ = oc.Close()
        }
    }
    connectionPool.Store(ip, conn)
    log.Printf("[pool] stored OUTGOING conn for %s (key=%s)\n", ip, ip)
}