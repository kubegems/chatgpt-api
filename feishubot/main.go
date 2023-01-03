package main

import (
	"chatbot/handlers"
	"os"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
)

func init() {
	switch os.Getenv("LOG_LEVEL") {
	case "DEBUG":
		log.SetLevel(log.DebugLevel)
	case "INFO":
		log.SetLevel(log.InfoLevel)
	case "ERROR":
		log.SetLevel(log.ErrorLevel)
	default:
		log.SetLevel(log.DebugLevel)
	}
}

func main() {
	loger := log.New()
	r := gin.Default()
	r.Use(ginlogrus.Logger(loger), gin.Recovery())
	feishu := handlers.NewFeishuHandler(handlers.NewFeishuOptions())
	feishu.StartEventLoop()
	r.POST("events", feishu.EventHandler())
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":9000"
	}
	r.Run(listen)
}
