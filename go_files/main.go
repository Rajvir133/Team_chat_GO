package main

import (
    "fmt"
    "log"
    "net/http"

    "go_files/config"
    "go_files/transfer"
)

func main() {
    
    go transfer.StartTCPServer(config.TCPPort)

    // HTTP routes
    http.HandleFunc("/send", SendHandler)
    http.HandleFunc("/scan", ScanHandler)

    log.Printf("🌐 HTTP server running on :%d\n", config.HTTPPort)
    err := http.ListenAndServe(fmt.Sprintf(":%d", config.HTTPPort), nil)
    if err != nil {
        log.Fatalf("HTTP server failed: %v", err)
    }
}
