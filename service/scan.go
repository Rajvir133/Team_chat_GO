package service

import (
	"fmt"
	"go_files/config"
	"net"
	"sync"
	"time"
)

func GetLocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

func GenerateIPRange(localIP string) []string {

	ip := net.ParseIP(localIP)
	if ip == nil {
		return []string{}
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		return []string{}
	}

	var ips []string
	baseIP := fmt.Sprintf("%d.%d.%d", ipv4[0], ipv4[1], ipv4[2])

	for i := 1; i <= 254; i++ {
		ips = append(ips, fmt.Sprintf("%s.%d", baseIP, i))
	}

	return ips
}

func ScanIPRange(ips []string) []string {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var foundDevices []string

	semaphore := make(chan struct{}, 50)

	for _, ip := range ips {
		wg.Add(1)
		go func(targetIP string) {
			defer wg.Done()

			semaphore <- struct{}{} // Acquire
			defer func() { <-semaphore }() // Release

			if tryConnect(targetIP) {
				mu.Lock()
				foundDevices = append(foundDevices, targetIP)
				mu.Unlock()

				config.DebugLog("Device found: %s", targetIP)
			}
		}(ip)
	}

	wg.Wait()
	return foundDevices
}

func tryConnect(ip string) bool {
	address := fmt.Sprintf("%s:%d", ip, config.TCPPort)
	
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return false
	}

	config.DebugLog("Successfully connected to %s (connection kept alive)", address)

	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				config.DebugLog("Scanner connection closed: %s", address)
				conn.Close()
				return
			}
		}
	}()
	
	return true
}