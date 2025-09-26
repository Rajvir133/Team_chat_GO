package main

import (
	"net"
	"strings"
	"sync"
	"time"
)

type PeerInfo struct {
	Hostname  string
	IP        string
	UpdatedAt time.Time
}

var (
	hostIndex sync.Map // key: lower(hostname) -> PeerInfo
	ipIndex   sync.Map // key: ip -> PeerInfo
)

func rememberPeer(ip, host string) {
	if ip == "" || host == "" {
		return
	}
	pi := PeerInfo{Hostname: host, IP: ip, UpdatedAt: time.Now()}
	hostIndex.Store(strings.ToLower(host), pi)
	ipIndex.Store(ip, pi)
}

// resolveFromRegistry returns IP for a hostname, or (if s is already an IP) the same string.
func resolveFromRegistry(s string) (string, bool) {
	if net.ParseIP(s) != nil {
		return s, true
	}
	if v, ok := hostIndex.Load(strings.ToLower(s)); ok {
		return v.(PeerInfo).IP, true
	}
	return "", false
}
