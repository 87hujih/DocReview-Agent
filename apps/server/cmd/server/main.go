package main

import (
	"log"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/router"
)

func main() {
	cfg := appconfig.Load()
	h := router.New(cfg)

	if err := h.Run(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
