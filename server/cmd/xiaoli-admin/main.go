package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mnhkahn/xiaoli-esp32/server/internal/admin"

	"github.com/mnhkahn/gogogo/logger"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	cfg := admin.LoadConfig()

	if err := os.MkdirAll(cfg.LogDir, 0755); err == nil {
		logPath := filepath.Join(cfg.LogDir, "server.log")
		jack := &lumberjack.Logger{
			Filename: logPath,
			MaxSize:  10, // megabytes
		}
		logger.SetOutput(io.MultiWriter(os.Stdout, jack))
		logger.Infof("Log output configured: stdout + %s", logPath)
	} else {
		logger.Warnf("Failed to create log dir %s: %v, logging to stdout only", cfg.LogDir, err)
	}
	if cfg.SessionSecret == "" || len(cfg.SessionSecret) < 32 {
		logger.Errorf("ADMIN_SESSION_SECRET must be at least 32 characters")
		os.Exit(1)
	}
	if cfg.LogtoEndpoint == "/" || cfg.LogtoAppID == "" || cfg.LogtoAppSecret == "" {
		logger.Errorf("LOGTO_ENDPOINT, LOGTO_APP_ID and LOGTO_APP_SECRET are required")
		os.Exit(1)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	server := admin.NewServer(cfg)
	server.StartBackground(context.Background())
	logger.Infof("Xiaoli Go admin listening on %s", addr)
	logger.Errorf("%v", http.ListenAndServe(addr, server))
	os.Exit(1)
}
