package ctl

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

// runPair performs interactive WhatsApp QR-code pairing. It opens the
// whatsmeow store directly (no running daemon required), displays the QR
// code in the terminal, waits for the user to scan it, persists the
// credentials into SQLite, and exits. The daemon can then connect using
// the stored credentials.
func runPair(args []string) error {
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "path to pantalk config")
	botName := flags.String("bot", "", "name of the whatsapp bot to pair")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *botName == "" {
		return fmt.Errorf("--bot is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var bot *config.BotConfig
	for i := range cfg.Bots {
		if cfg.Bots[i].Name == *botName {
			bot = &cfg.Bots[i]
			break
		}
	}
	if bot == nil {
		return fmt.Errorf("bot %q not found in config", *botName)
	}
	if bot.Type != "whatsapp" {
		return fmt.Errorf("bot %q is type %q — pair is only for whatsapp bots", *botName, bot.Type)
	}

	dbPath := strings.TrimSpace(bot.DBPath)
	if dbPath == "" {
		dataDir := filepath.Dir(config.DefaultDBPath())
		dbPath = filepath.Join(dataDir, fmt.Sprintf("whatsapp-%s.db", bot.Name))
	}
	if err := config.EnsureDir(dbPath); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger := waLog.Stdout("WhatsApp", "ERROR", true)
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)
	container, err := sqlstore.New(ctx, "sqlite3", dsn, logger)
	if err != nil {
		return fmt.Errorf("open whatsapp store: %w", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}

	if device.ID != nil {
		fmt.Fprintf(os.Stderr, "bot %q is already paired (jid=%s)\n", *botName, device.ID.String())
		fmt.Fprintf(os.Stderr, "to re-pair, delete %s and run this command again\n", dbPath)
		return nil
	}

	client := whatsmeow.NewClient(device, logger)

	qrChan, _ := client.GetQRChannel(ctx)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	fmt.Fprintln(os.Stderr, "scan this QR code with WhatsApp on your phone:")
	fmt.Fprintln(os.Stderr, "(Settings → Linked Devices → Link a Device)")
	fmt.Fprintln(os.Stderr)

	for evt := range qrChan {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted")
		default:
		}

		switch evt.Event {
		case "code":
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stderr)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "waiting for scan...")
		case "success":
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "paired successfully! credentials saved to %s\n", dbPath)

			// Try to reload the daemon so it picks up credentials immediately.
			// This is best-effort — the daemon may not be running yet.
			socketPath := cfg.Server.SocketPath
			if socketPath == "" {
				socketPath = defaultSocketPath
			}
			resp, err := call(socketPath, protocol.Request{Action: protocol.ActionReload})
			if err == nil && resp.OK {
				fmt.Fprintln(os.Stderr, "daemon reloaded — connecting now")
			}

			return nil
		case "timeout":
			return fmt.Errorf("QR code timed out — run this command again to retry")
		}
	}

	return fmt.Errorf("pairing channel closed unexpectedly")
}
