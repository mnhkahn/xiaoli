package main

import (
	"context"
	"os"

	"xiaoli/server/internal/admin"

	"github.com/mnhkahn/gogogo/logger"
)

func main() {
	ctx := context.Background()
	err := admin.WechatLoginCLI(ctx)
	if err != nil {
		logger.Errorf("login failed: %v", err)
		os.Exit(1)
	}
}
