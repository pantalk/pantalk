package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/chatbotkit/pantalk/internal/config"
	"github.com/chatbotkit/pantalk/internal/server"
	"github.com/chatbotkit/pantalk/internal/version"
)

func main() {
	configPath := flag.String("config", "", "path to pantalk config (default: "+config.DefaultConfigPath()+")")
	socketPath := flag.String("socket", "", "override unix socket path (defaults to config value)")
	databasePath := flag.String("db", "", "override pantalk sqlite database path (defaults to config value)")
	debug := flag.Bool("debug", false, "enable verbose debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("pantalkd %s\n", version.Version)

		if result, err := version.Check(); err == nil {
			if notice := version.FormatUpdateNotice(result); notice != "" {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, notice)
			}
		}

		os.Exit(0)
	}

	// Log version at startup so operators can see which build is running.
	log.Printf("pantalkd %s starting", version.Version)

	// Check for updates at startup (non-blocking, best-effort).
	if !version.IsDev() {
		if result, err := version.Check(); err == nil {
			if notice := version.FormatUpdateNotice(result); notice != "" {
				log.Println(notice)
			}
		}
	}

	if *configPath == "" {
		*configPath = config.DefaultConfigPath()
	}

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
	srv.SetDebug(*debug)
	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
