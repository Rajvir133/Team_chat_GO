package main

import (
	"fmt"
	"go_files/config"
	"go_files/service"

	"github.com/gin-gonic/gin"
)

func main() {
	
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	router.Use(config.SetupCORS())

	fmt.Println("----------------------------------------")
	go func() {
		config.InfoLog("HTTP on port %d", config.HTTPPort)
		router.Run(fmt.Sprintf(":%d", config.HTTPPort))
	}()

	service.StartTCPServer()
	fmt.Println("----------------------------------------")
}