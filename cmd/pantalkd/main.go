package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/server"
)

func main() {
	configPath := flag.String("config", "./configs/pantalk.yaml", "path to pantalk config")
	socketPath := flag.String("socket", "", "override unix socket path (defaults to config value)")
	databasePath := flag.String("db", "", "override pantalk sqlite database path (defaults to config value)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if *socketPath != "" {
		cfg.Server.SocketPath = *socketPath
	}

	if *databasePath != "" {
		cfg.Server.DBPath = *databasePath
	}

	srv := server.New(cfg, *configPath, *socketPath, *databasePath)
	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
