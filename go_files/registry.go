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
	Tag       string // e.g., "BMS", "CMK"
	Android   bool   // true if Tag == "CMK" or we marked it explicitly
	UpdatedAt time.Time
}

var (
	hostIndex sync.Map // key: lower(hostname) -> PeerInfo
	ipIndex   sync.Map // key: ip             -> PeerInfo
)

// ---- Public helpers used elsewhere ----

func rememberPeer(ip, host string) {
	rememberPeerWithTag(ip, host, "")
}

func rememberPeerWithTag(ip, host, tag string) {
	if ip == "" && net.ParseIP(host) != nil {
		// (defensive) caller might have swapped; normalize
		ip, host = host, ip
	}
	pi := PeerInfo{
		Hostname:  host,
		IP:        ip,
		Tag:       tag,
		Android:   strings.EqualFold(tag, "CMK"),
		UpdatedAt: time.Now(),
	}
	// Preserve prior tag/android if not provided this time
	if v, ok := ipIndex.Load(ip); ok {
		old := v.(PeerInfo)
		if pi.Tag == "" {
			pi.Tag = old.Tag
		}
		if !pi.Android {
			pi.Android = old.Android
		}
		if pi.Hostname == "" {
			pi.Hostname = old.Hostname
		}
	}
	ipIndex.Store(ip, pi)
	if host != "" {
		hostIndex.Store(strings.ToLower(host), pi)
	}
}

func markAndroidPeer(ip string, isAndroid bool) {
	if v, ok := ipIndex.Load(ip); ok {
		pi := v.(PeerInfo)
		pi.Android = isAndroid
		ipIndex.Store(ip, pi)
		if pi.Hostname != "" {
			hostIndex.Store(strings.ToLower(pi.Hostname), pi)
		}
	} else {
		ipIndex.Store(ip, PeerInfo{IP: ip, Android: isAndroid, UpdatedAt: time.Now()})
	}
}

func isAndroidPeer(ip string) bool {
	if v, ok := ipIndex.Load(ip); ok {
		return v.(PeerInfo).Android
	}
	return false
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

func getPeer(ip string) (PeerInfo, bool) {
	if v, ok := ipIndex.Load(ip); ok {
		return v.(PeerInfo), true
	}
	return PeerInfo{}, false
}

func getPeerByHost(host string) (PeerInfo, bool) {
	if v, ok := hostIndex.Load(strings.ToLower(host)); ok {
		return v.(PeerInfo), true
	}
	return PeerInfo{}, false
}
