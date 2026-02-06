package main

import (
	"go_files/config"
	"go_files/service"

	"github.com/gin-gonic/gin"
)

type ScanResponse struct {
	TotalScanned int      `json:"total_scanned"`
	DevicesFound int      `json:"devices_found"`
	Devices      []string `json:"devices"`
}
type ConnectionInfo struct {
	Address  string `json:"address"`
	UserName string `json:"user_name"`
}

func ScanNetwork(c *gin.Context) {
	config.InfoLog("Network scan initiated")

	localIP, err := service.GetLocalIP()
	if err != nil {
		config.ErrorLog("Failed to get local IP: %v", err)
		c.JSON(500, gin.H{"error": "Failed to get local IP"})
		return
	}

	config.DebugLog("Local IP: %s", localIP)

	ipRange := service.GenerateIPRange(localIP)
	config.DebugLog("Scanning %d IPs in range", len(ipRange))

	devices := service.ScanIPRange(ipRange)

	response := ScanResponse{
		TotalScanned: len(ipRange),
		DevicesFound: len(devices),
		Devices:      devices,
	}

	config.InfoLog("Scan completed - Found %d devices", len(devices))

	c.JSON(200, response)
}

func FetchConnections(c *gin.Context) {
	config.DebugLog("Fetching all connections from pool")

	allConnections := service.Pool.ListAllConnections()

	var connections []ConnectionInfo
	for address, conn := range allConnections {
		connections = append(connections, ConnectionInfo{
			Address:  address,
			UserName: conn.UserName,
		})
	}

	config.DebugLog("Retrieved %d connections", len(connections))

	c.JSON(200, gin.H{
		"total":       len(connections),
		"connections": connections,
	})
}