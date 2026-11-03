package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	router := gin.Default()
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("启动 Web 服务失败: %v", err)
	}
}
