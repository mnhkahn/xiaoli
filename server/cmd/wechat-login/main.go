package main

import (
	"context"
	"log"

	"xiaoli/server/internal/admin"
)

func main() {
	ctx := context.Background()
	err := admin.WechatLoginCLI(ctx)
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}
}