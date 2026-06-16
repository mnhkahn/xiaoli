package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"xiaoli/server/internal/admin"

	"github.com/mnhkahn/gogogo/logger"
)

func main() {
	cfg := admin.LoadConfig()
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
