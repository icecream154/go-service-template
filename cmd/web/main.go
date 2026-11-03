package main

import (
	"log"
	"net/http"

	"go-service-template/bootstrap"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := bootstrap.Init(); err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	router := gin.Default()
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}
