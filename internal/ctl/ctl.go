package ctl

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/protocol"
)

var defaultConfigPath = config.DefaultConfigPath()
var defaultSocketPath = config.DefaultSocketPath()

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "setup":
		return runSetup(subArgs)
	case "validate":
		return runValidate(subArgs)
	case "reload":
		return runReload(subArgs)
	case "config":
		return runConfig(subArgs)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", subcommand)
	}
}

func runSetup(args []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	output := flags.String("output", defaultConfigPath, "output config path")
	force := flags.Bool("force", false, "overwrite output file if it exists")
	if err := flags.Parse(args); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	printSetupIntro()

	cfg, err := runWizard(reader)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	if fileExists(*output) && !*force {
		overwrite, askErr := promptYesNo(reader, fmt.Sprintf("file %s exists, overwrite?", *output), false)
		if askErr != nil {
			return askErr
		}
		if !overwrite {
			return errors.New("aborted by user")
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(*output, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("wrote config to %s\n", *output)
	return nil
}

func runValidate(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "config path to validate")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if _, err := config.Load(*configPath); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	fmt.Printf("config is valid: %s\n", *configPath)
	return nil
}

func runReload(args []string) error {
	flags := flag.NewFlagSet("reload", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	resp, err := call(*socket, protocol.Request{Action: protocol.ActionReload})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}

	fmt.Println(resp.Ack)
	return nil
}

func runConfig(args []string) error {
	if len(args) == 0 {
		printConfigUsage()
		return nil
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "print":
		return runConfigPrint(subArgs)
	case "set-server":
		return runConfigSetServer(subArgs)
	case "add-bot":
		return runConfigAddBot(subArgs)
	case "remove-bot":
		return runConfigRemoveBot(subArgs)
	case "help", "-h", "--help":
		printConfigUsage()
		return nil
	default:
		return fmt.Errorf("unknown config command %q", subcommand)
	}
}

func runConfigPrint(args []string) error {
	flags := flag.NewFlagSet("config print", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "config path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	_, err = config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	fmt.Print(string(data))
	return nil
}

func runConfigSetServer(args []string) error {
	flags := flag.NewFlagSet("config set-server", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "config path")
	socket := flags.String("socket", "", "set server.socket_path")
	db := flags.String("db", "", "set server.db_path")
	history := flags.Int("history", -1, "set server.notification_history_size")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*socket) == "" && strings.TrimSpace(*db) == "" && *history < 0 {
		return errors.New("no changes requested: provide --socket, --db, and/or --history")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if strings.TrimSpace(*socket) != "" {
		cfg.Server.SocketPath = strings.TrimSpace(*socket)
	}
	if strings.TrimSpace(*db) != "" {
		cfg.Server.DBPath = strings.TrimSpace(*db)
	}
	if *history >= 0 {
		if *history == 0 {
			return errors.New("history must be > 0")
		}
		cfg.Server.HistorySize = *history
	}

	if err := saveConfigValidated(*configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("updated server config in %s\n", *configPath)
	return nil
}

func runConfigAddBot(args []string) error {
	flags := flag.NewFlagSet("config add-bot", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "config path")
	name := flags.String("name", "", "bot name")
	botType := flags.String("type", "", "bot type (slack, discord, mattermost, telegram)")
	botToken := flags.String("bot-token", "", "bot_token (literal or $ENV_VAR)")
	appLevelToken := flags.String("app-level-token", "", "app_level_token (slack only)")
	transport := flags.String("transport", "", "custom transport (for non-built-in types)")
	endpoint := flags.String("endpoint", "", "endpoint (required for mattermost/custom)")
	channels := flags.String("channels", "", "comma-separated channels")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*botType) == "" {
		return errors.New("--name and --type are required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	for _, existingBot := range cfg.Bots {
		if existingBot.Name == strings.TrimSpace(*name) {
			return fmt.Errorf("bot %q already exists", *name)
		}
	}

	cfg.Bots = append(cfg.Bots, config.BotConfig{
		Name:          strings.TrimSpace(*name),
		Type:          strings.TrimSpace(*botType),
		BotToken:      strings.TrimSpace(*botToken),
		AppLevelToken: strings.TrimSpace(*appLevelToken),
		Transport:     strings.TrimSpace(*transport),
		Endpoint:      strings.TrimSpace(*endpoint),
		Channels:      splitCSV(*channels),
	})

	if err := saveConfigValidated(*configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("added bot %s (type: %s)\n", *name, *botType)
	return nil
}

func runConfigRemoveBot(args []string) error {
	flags := flag.NewFlagSet("config remove-bot", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath, "config path")
	name := flags.String("name", "", "bot name")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*name) == "" {
		return errors.New("--name is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	updated := make([]config.BotConfig, 0, len(cfg.Bots))
	removed := false
	for _, bot := range cfg.Bots {
		if bot.Name == strings.TrimSpace(*name) {
			removed = true
			continue
		}
		updated = append(updated, bot)
	}

	if !removed {
		return fmt.Errorf("bot %q not found", *name)
	}

	cfg.Bots = updated
	if err := saveConfigValidated(*configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("removed bot %s\n", *name)
	return nil
}

func runWizard(reader *bufio.Reader) (config.Config, error) {
	socketPath, err := promptText(reader, "server socket path", config.DefaultSocketPath(), true)
	if err != nil {
		return config.Config{}, err
	}

	dbPath, err := promptText(reader, "server db path", config.DefaultDBPath(), true)
	if err != nil {
		return config.Config{}, err
	}

	historySizeRaw, err := promptText(reader, "notification history size", "1000", true)
	if err != nil {
		return config.Config{}, err
	}

	historySize, err := strconv.Atoi(historySizeRaw)
	if err != nil || historySize <= 0 {
		return config.Config{}, errors.New("notification history size must be a positive integer")
	}

	bots := make([]config.BotConfig, 0)
	for {
		provider, chooseErr := chooseProvider(reader)
		if chooseErr != nil {
			return config.Config{}, chooseErr
		}
		if provider == "done" {
			if len(bots) == 0 {
				fmt.Println("add at least one bot")
				continue
			}
			break
		}

		bot, buildErr := buildBot(reader, provider)
		if buildErr != nil {
			return config.Config{}, buildErr
		}
		bots = append(bots, bot)

		addMore, addErr := promptYesNo(reader, "add another bot?", false)
		if addErr != nil {
			return config.Config{}, addErr
		}
		if !addMore {
			break
		}
	}

	return config.Config{
		Server: config.ServerConfig{
			SocketPath:  socketPath,
			DBPath:      dbPath,
			HistorySize: historySize,
		},
		Bots: bots,
	}, nil
}

func buildBot(reader *bufio.Reader, provider string) (config.BotConfig, error) {
	botName, err := promptText(reader, fmt.Sprintf("%s bot name", provider), provider+"-bot", true)
	if err != nil {
		return config.BotConfig{}, err
	}

	botTokenPrompt := fmt.Sprintf("%s bot_token (literal or $ENV_VAR)", provider)
	botToken, err := promptText(reader, botTokenPrompt, "$"+strings.ToUpper(provider)+"_BOT_TOKEN", true)
	if err != nil {
		return config.BotConfig{}, err
	}

	b := config.BotConfig{
		Name:     botName,
		Type:     provider,
		BotToken: botToken,
	}

	if provider == "slack" {
		appToken, appErr := promptText(reader, "slack app_level_token (literal or $ENV_VAR)", "$SLACK_APP_LEVEL_TOKEN", true)
		if appErr != nil {
			return config.BotConfig{}, appErr
		}
		b.AppLevelToken = appToken
	}

	if provider == "mattermost" {
		endpoint, endpointErr := promptText(reader, "mattermost endpoint", "https://mattermost.example.com", true)
		if endpointErr != nil {
			return config.BotConfig{}, endpointErr
		}
		b.Endpoint = endpoint
	}

	channelsRaw, channelsErr := promptText(reader, fmt.Sprintf("%s channels (comma-separated, empty for all)", provider), "", false)
	if channelsErr != nil {
		return config.BotConfig{}, channelsErr
	}
	b.Channels = splitCSV(channelsRaw)

	return b, nil
}

func chooseProvider(reader *bufio.Reader) (string, error) {
	fmt.Println("\nSelect a bot type to configure:")
	fmt.Println("  1) slack")
	fmt.Println("  2) discord")
	fmt.Println("  3) mattermost")
	fmt.Println("  4) telegram")
	fmt.Println("  5) done")

	choice, err := promptText(reader, "choice", "1", true)
	if err != nil {
		return "", err
	}

	switch strings.TrimSpace(choice) {
	case "1", "slack":
		return "slack", nil
	case "2", "discord":
		return "discord", nil
	case "3", "mattermost":
		return "mattermost", nil
	case "4", "telegram":
		return "telegram", nil
	case "5", "done":
		return "done", nil
	default:
		return "", errors.New("invalid choice")
	}
}

func promptText(reader *bufio.Reader, label string, defaultValue string, required bool) (string, error) {
	for {
		if defaultValue != "" {
			fmt.Printf("%s [%s]: ", label, defaultValue)
		} else {
			fmt.Printf("%s: ", label)
		}

		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		value := strings.TrimSpace(input)
		if value == "" {
			value = defaultValue
		}

		if required && strings.TrimSpace(value) == "" {
			fmt.Println("value is required")
			continue
		}

		return value, nil
	}
}

func promptYesNo(reader *bufio.Reader, label string, defaultYes bool) (bool, error) {
	defaultLabel := "y/N"
	if defaultYes {
		defaultLabel = "Y/n"
	}

	for {
		fmt.Printf("%s [%s]: ", label, defaultLabel)
		input, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}

		value := strings.ToLower(strings.TrimSpace(input))
		if value == "" {
			return defaultYes, nil
		}

		if value == "y" || value == "yes" {
			return true, nil
		}
		if value == "n" || value == "no" {
			return false, nil
		}

		fmt.Println("please answer yes or no")
	}
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func saveConfigValidated(path string, cfg config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}

	if _, err := config.Load(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("resulting config is invalid: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace config: %w", err)
	}

	return nil
}

func call(socket string, request protocol.Request) (protocol.Response, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("connect socket: %w", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return protocol.Response{}, fmt.Errorf("send request: %w", err)
	}

	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return protocol.Response{}, fmt.Errorf("read response: %w", err)
	}

	return resp, nil
}

func printSetupIntro() {
	// Target color: teal accent RGB(36, 219, 198)
	const tr, tg, tb = 36, 219, 198
	const reset = "\033[0m"
	const hideCursor = "\033[?25l"
	const showCursor = "\033[?25h"

	mascotLines := []string{
		`                                       =====                                       `,
		`                                ===================                                `,
		`                             =========================                             `,
		`                          ========               ========                          `,
		`                        =======                     =======                        `,
		`                      ======                           ======                      `,
		`                     =====                               =====                     `,
		`                    =====                                 =====                    `,
		`                   =====                                    ====                   `,
		`                  ====                                       ====                  `,
		`                 =====                                       =====                 `,
		`                 ====            =               =            ====                 `,
		`            =========         =======         =======         =========            `,
		`          ===========        =========       =========        ===========          `,
		`         ====    ====        =========       =========        ====    ====         `,
		`         ===     ====         =======         =======         ====     ===         `,
		`         ===     ====           ===              ==           ====     ===         `,
		`         ===     ====                                         ====     ===         `,
		`         ===     ====                                         ====     ===         `,
		`         ===     ====                                         ====     ===         `,
		`         ====    ====          =====           =====          ====    ====         `,
		`          ===========           =======     =======           ===========          `,
		`             ========             ===============             ========             `,
		`                 ====                +========                ====                 `,
		`                 ====                                         ====                 `,
		`                 ====                                         ====                 `,
		`                 ====                                         ====                 `,
		`                 ====              =======              ==========                 `,
		`                 ======          ===========          ============                 `,
		`                 ========      ======   ======      ======   =====                 `,
		`                 === ==============       =============        ===                 `,
		`                       ==========           ==========                             `,
		`                           ==                   ==                                 `,
	}

	// Hide cursor during animation.
	fmt.Print(hideCursor)

	// Phase 1: reveal lines top-to-bottom at dim brightness, accelerating.
	for i, line := range mascotLines {
		// Ramp brightness from ~30% to ~70% during reveal.
		frac := float64(i) / float64(len(mascotLines)-1)
		brightness := 0.3 + 0.4*frac
		r := int(float64(tr) * brightness)
		g := int(float64(tg) * brightness)
		b := int(float64(tb) * brightness)
		fmt.Printf("\033[38;2;%d;%d;%dm%s%s\n", r, g, b, line, reset)
		// Start slow (30ms), speed up to 10ms.
		delay := time.Duration(30-int(20*frac)) * time.Millisecond
		time.Sleep(delay)
	}

	// Phase 2: "glow up" - redraw the whole mascot brightening to full color.
	steps := 6
	for s := 1; s <= steps; s++ {
		// Move cursor up to top of mascot.
		fmt.Printf("\033[%dA", len(mascotLines))
		brightness := 0.7 + 0.3*float64(s)/float64(steps)
		r := int(float64(tr) * brightness)
		g := int(float64(tg) * brightness)
		b := int(float64(tb) * brightness)
		if r > tr {
			r = tr
		}
		if g > tg {
			g = tg
		}
		if b > tb {
			b = tb
		}
		for _, line := range mascotLines {
			fmt.Printf("\033[38;2;%d;%d;%dm%s%s\n", r, g, b, line, reset)
		}
		time.Sleep(40 * time.Millisecond)
	}

	// Show cursor again.
	fmt.Print(showCursor)

	fmt.Println()
	fmt.Println("Pantalk Setup Wizard")
	fmt.Println("--------------------")
	fmt.Println("This interactive setup writes a strict pantalk config file.")
	fmt.Println("Token fields accept literal values or env references like $SLACK_BOT_TOKEN.")
	fmt.Println()
}

func printUsage() {
	fmt.Printf(`pantalk admin commands

Usage:
  pantalk setup [--output %s] [--force]
  pantalk validate [--config %s]
  pantalk reload [--socket %s]
  pantalk config <subcommand> [options]
  pantalk help
`, defaultConfigPath, defaultConfigPath, defaultSocketPath)
}

func printConfigUsage() {
	fmt.Printf(`pantalk config commands

Usage:
  pantalk config print [--config %s]
  pantalk config set-server --config <path> [--socket ...] [--db ...] [--history ...]
  pantalk config add-bot --config <path> --name <bot> --type <type> [--bot-token ...] [--app-level-token ...] [--endpoint ...] [--transport ...] [--channels a,b]
  pantalk config remove-bot --config <path> --name <bot>
`, defaultConfigPath)
}
