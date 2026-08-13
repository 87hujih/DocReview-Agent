package main

import (
	"context"
	"log"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/storage/postgres"
)

// main 为 the only production entrypoint authorized 用于 apply embedded 数据库 migrations.
func main() {
	cfg := appconfig.Load()
	if err := cfg.ValidateForMigrations(); err != nil {
		log.Fatalf("配置无效：%v", err)
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败：%v", err)
	}
	defer pool.Close()

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("数据库迁移失败：%v", err)
	}
	log.Print("数据库迁移完成")
}
