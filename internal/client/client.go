package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/ctl"
	"github.com/pantalk/pantalk/internal/protocol"
	"github.com/pantalk/pantalk/internal/server"
	"github.com/pantalk/pantalk/internal/skill"
)

var defaultSocketPath = config.DefaultSocketPath()

// isTTY returns true if stdout is connected to a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// isStdinTTY returns true if stdin is connected to a terminal.
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// readStdin reads all of stdin and returns the trimmed content.
func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func Run(service string, toolName string, args []string) int {
	if len(args) == 0 {
		printUsage(toolName)
		return 2
	}

	command := args[0]
	commandArgs := args[1:]

	switch command {
	case "bots":
		return runBots(service, commandArgs)
	case "status":
		return runStatus(service, commandArgs)
	case "send":
		return runSend(service, commandArgs)
	case "inject":
		return runInject(service, commandArgs)
	case "chat":
		return runChat(service, commandArgs)
	case "local":
		return runLocal(commandArgs)
	case "typing":
		return runTyping(service, args[1:])
	case "react":
		return runReact(service, commandArgs)
	case "history":
		return runHistory(service, commandArgs, false)
	case "notifications", "notify":
		return runHistory(service, commandArgs, true)
	case "stream", "subscribe":
		return runSubscribe(service, commandArgs)
	case "ping":
		return runPing(commandArgs)
	case "skill":
		if err := skill.Run(commandArgs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "setup", "validate", "reload", "config", "pair":
		if err := ctl.Run(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		printUsage(toolName)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		printUsage(toolName)
		return 2
	}
}

const (
	localBotName = "local-test"
)

type localOptions struct {
	workdir        string
	user           string
	driver         string
	statePath      string
	socketPath     string
	codexBinary    string
	claudeBinary   string
	model          string
	effort         string
	sandbox        string
	approval       string
	permissionMode string
	instructions   string
	timeout        int
	ephemeral      bool
	debug          bool
	stateExplicit  bool
	socketExplicit bool
}

type embeddedLocalServer interface {
	Ready() <-chan struct{}
	RunContext(context.Context) error
}

type localDependencies struct {
	newServer func(config.Config, bool) embeddedLocalServer
	runChat   func(string, string) int
}

func defaultLocalDependencies() localDependencies {
	return localDependencies{
		newServer: func(cfg config.Config, debug bool) embeddedLocalServer {
			srv := server.New(cfg, "", "", "")
			srv.SetDebug(debug)
			return srv
		},
		runChat: func(socketPath string, user string) int {
			return runChat("local", []string{
				"--socket", socketPath,
				"--bot", localBotName,
				"--user", user,
			})
		},
	}
}

func defaultLocalStatePath() string {
	return filepath.Join(filepath.Dir(config.DefaultDBPath()), "local.db")
}

func defaultLocalSocketPath() string {
	socketPath := config.DefaultSocketPath()
	extension := filepath.Ext(socketPath)
	name := strings.TrimSuffix(filepath.Base(socketPath), extension)
	return filepath.Join(filepath.Dir(socketPath), name+"-local"+extension)
}

func runLocal(args []string) int {
	flags := flag.NewFlagSet("local", flag.ContinueOnError)
	workdir := flags.String("workdir", ".", "working directory presented to the agent")
	user := flags.String("user", "local-user", "local chat user id")
	driver := flags.String("driver", "codex", "conversational agent driver (codex or claude)")
	statePath := flags.String("state", defaultLocalStatePath(), "SQLite state path")
	socketPath := flags.String("socket", defaultLocalSocketPath(), "Unix socket path")
	codexBinary := flags.String("codex-binary", "", "Codex executable (default: codex on PATH)")
	claudeBinary := flags.String("claude-binary", "", "Claude Code executable (default: claude on PATH)")
	model := flags.String("model", "", "agent model override (default: inherit local config)")
	effort := flags.String("effort", "", "agent effort override")
	sandbox := flags.String("sandbox", "read-only", "Codex sandbox: read-only, workspace-write, or danger-full-access")
	approval := flags.String("approval-policy", "never", "Codex approval policy: untrusted, on-request, or never")
	permissionMode := flags.String("permission-mode", "plan", "Claude permission mode: plan, dontAsk, acceptEdits, auto, manual, or bypassPermissions")
	instructions := flags.String("instructions", "", "agent instructions")
	timeout := flags.Int("timeout", 900, "maximum seconds per agent turn")
	ephemeral := flags.Bool("ephemeral", false, "discard socket, database, and sessions on exit")
	debug := flags.Bool("debug", false, "enable verbose Pantalk server logging")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	options := localOptions{
		workdir:        *workdir,
		user:           *user,
		driver:         *driver,
		statePath:      *statePath,
		socketPath:     *socketPath,
		codexBinary:    *codexBinary,
		claudeBinary:   *claudeBinary,
		model:          *model,
		effort:         *effort,
		sandbox:        *sandbox,
		approval:       *approval,
		permissionMode: *permissionMode,
		instructions:   *instructions,
		timeout:        *timeout,
		ephemeral:      *ephemeral,
		debug:          *debug,
		stateExplicit:  visited["state"],
		socketExplicit: visited["socket"],
	}

	if err := runLocalMode(options, defaultLocalDependencies()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runLocalMode(options localOptions, dependencies localDependencies) error {
	options.driver = strings.TrimSpace(options.driver)
	if options.driver != "codex" && options.driver != "claude" {
		return fmt.Errorf("unsupported --driver %q (use codex or claude)", options.driver)
	}
	options.user = strings.TrimSpace(options.user)
	if options.user == "" {
		return errors.New("--user cannot be empty")
	}
	if options.timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	if options.driver == "codex" {
		switch options.sandbox {
		case "read-only", "workspace-write", "danger-full-access":
		default:
			return fmt.Errorf("unsupported --sandbox %q", options.sandbox)
		}
		switch options.approval {
		case "untrusted", "on-request", "never":
		default:
			return fmt.Errorf("unsupported --approval-policy %q", options.approval)
		}
	} else {
		switch options.permissionMode {
		case "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan":
		default:
			return fmt.Errorf("unsupported --permission-mode %q", options.permissionMode)
		}
	}
	if options.ephemeral && (options.stateExplicit || options.socketExplicit) {
		return errors.New("--ephemeral cannot be combined with --state or --socket")
	}

	workdir, err := filepath.Abs(strings.TrimSpace(options.workdir))
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}
	info, err := os.Stat(workdir)
	if err != nil {
		return fmt.Errorf("workdir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workdir %q is not a directory", workdir)
	}

	var ephemeralDir string
	if options.ephemeral {
		ephemeralDir, err = os.MkdirTemp("", "pantalk-local-")
		if err != nil {
			return fmt.Errorf("create ephemeral state directory: %w", err)
		}
		defer os.RemoveAll(ephemeralDir)
		options.statePath = filepath.Join(ephemeralDir, "pantalk.db")
		options.socketPath = filepath.Join(ephemeralDir, "pantalk.sock")
	}

	options.statePath = strings.TrimSpace(options.statePath)
	options.socketPath = strings.TrimSpace(options.socketPath)
	if options.statePath == "" || options.socketPath == "" {
		return errors.New("--state and --socket cannot be empty")
	}
	if err := config.EnsureDir(options.socketPath); err != nil {
		return fmt.Errorf("prepare local socket directory: %w", err)
	}

	if connection, dialErr := net.DialTimeout("unix", options.socketPath, 150*time.Millisecond); dialErr == nil {
		_ = connection.Close()
		return fmt.Errorf("another Pantalk local session is already listening on %s", options.socketPath)
	}

	instructions := strings.TrimSpace(options.instructions)
	if instructions == "" {
		instructions = "You are participating in a local Pantalk test conversation. Answer the user directly and concisely. Do not modify files unless explicitly requested."
	}

	agentConfig := config.AgentConfig{
		Name:         "local-" + options.driver,
		Driver:       options.driver,
		Workdir:      workdir,
		Instructions: instructions,
		Timeout:      options.timeout,
	}
	if options.driver == "codex" {
		agentConfig.Codex = config.CodexAgentConfig{
			Binary:         strings.TrimSpace(options.codexBinary),
			Model:          strings.TrimSpace(options.model),
			Effort:         strings.TrimSpace(options.effort),
			Sandbox:        options.sandbox,
			ApprovalPolicy: options.approval,
		}
	} else {
		agentConfig.Claude = config.ClaudeAgentConfig{
			Binary:         strings.TrimSpace(options.claudeBinary),
			Model:          strings.TrimSpace(options.model),
			Effort:         strings.TrimSpace(options.effort),
			PermissionMode: options.permissionMode,
		}
	}

	cfg := config.Config{
		Server: config.ServerConfig{
			SocketPath:  options.socketPath,
			DBPath:      options.statePath,
			HistorySize: 500,
			Media: config.MediaConfig{
				Backend: config.MediaBackendNone,
			},
		},
		Bots: []config.BotConfig{{
			Name:        localBotName,
			Type:        "local",
			DisplayName: "Pantalk Local",
			Agents: []config.BotAgentBinding{{
				Agent: agentConfig.Name,
				When:  "notify",
			}},
		}},
		Agents: []config.AgentConfig{agentConfig},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	localServer := dependencies.newServer(cfg, options.debug)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- localServer.RunContext(ctx)
	}()

	select {
	case <-localServer.Ready():
	case err := <-serverDone:
		if err == nil {
			return errors.New("Pantalk local server stopped before becoming ready")
		}
		return fmt.Errorf("start Pantalk local server: %w", err)
	case <-ctx.Done():
		<-serverDone
		return nil
	}

	fmt.Fprintf(os.Stderr, "Pantalk local mode ready\n")
	fmt.Fprintf(os.Stderr, "  agent:   %s\n", options.driver)
	fmt.Fprintf(os.Stderr, "  workdir: %s\n", workdir)
	if options.driver == "codex" {
		fmt.Fprintf(os.Stderr, "  sandbox: %s\n", options.sandbox)
	} else {
		fmt.Fprintf(os.Stderr, "  permissions: %s\n", options.permissionMode)
	}
	if options.ephemeral {
		fmt.Fprintln(os.Stderr, "  state:   ephemeral")
	} else {
		fmt.Fprintf(os.Stderr, "  state:   %s\n", options.statePath)
	}

	chatDone := make(chan int, 1)
	go func() {
		chatDone <- dependencies.runChat(options.socketPath, options.user)
	}()

	var chatCode int
	select {
	case chatCode = <-chatDone:
		cancel()
	case err := <-serverDone:
		cancel()
		if err != nil {
			return fmt.Errorf("Pantalk local server stopped: %w", err)
		}
		return errors.New("Pantalk local server stopped unexpectedly")
	case <-ctx.Done():
		chatCode = 0
	}

	serverErr := <-serverDone
	if serverErr != nil {
		return fmt.Errorf("stop Pantalk local server: %w", serverErr)
	}
	if chatCode != 0 {
		return fmt.Errorf("local chat exited with status %d", chatCode)
	}
	return nil
}

// runInject sends one synthetic inbound message to a local connector. The
// daemon rejects this action for every network-backed connector.
func runInject(service string, args []string) int {
	flags := flag.NewFlagSet("inject", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (normally local; auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "local bot name from config")
	user := flags.String("user", "", "inbound sender id")
	self := flags.Bool("self", false, "inject as the local bot identity (tests loop prevention)")
	target := flags.String("target", "", "generic destination id")
	channel := flags.String("channel", "", "channel id")
	thread := flags.String("thread", "", "thread id")
	message := flags.String("text", "", "message text (use - to read from stdin)")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*bot) == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}
	if strings.TrimSpace(*user) == "" && !*self {
		fmt.Fprintln(os.Stderr, "--user is required (or use --self)")
		return 2
	}

	text := *message
	if text == "-" || (text == "" && !isStdinTTY()) {
		stdinText, err := readStdin()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		text = stdinText
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "--text is required (or pass message via stdin)")
		return 2
	}

	// A missing destination means a direct conversation with this sender.
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		sender := strings.TrimSpace(*user)
		if *self {
			sender = "local:" + strings.TrimSpace(*bot)
		}
		*target = "user:" + sender
	}

	resp, err := call(*socket, protocol.Request{
		Action:  protocol.ActionInject,
		Service: resolveService(service, *svcFlag),
		Bot:     strings.TrimSpace(*bot),
		User:    strings.TrimSpace(*user),
		Self:    *self,
		Target:  strings.TrimSpace(*target),
		Channel: strings.TrimSpace(*channel),
		Thread:  strings.TrimSpace(*thread),
		Text:    text,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if resp.Event != nil {
		if *jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(resp.Event)
		} else {
			printEvent(*resp.Event)
		}
	}

	return 0
}

// runChat provides a small terminal client over the local connector. It opens
// the subscription before accepting input so an immediate agent reply cannot
// race past the client.
func runChat(service string, args []string) int {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (normally local; auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "local bot name from config")
	user := flags.String("user", "local-user", "inbound sender id")
	target := flags.String("target", "", "generic destination id")
	channel := flags.String("channel", "", "channel id")
	thread := flags.String("thread", "", "thread id")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	*bot = strings.TrimSpace(*bot)
	*user = strings.TrimSpace(*user)
	if *bot == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}
	if *user == "" {
		fmt.Fprintln(os.Stderr, "--user cannot be empty")
		return 2
	}
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		*target = "user:" + *user
	}

	svc := resolveService(service, *svcFlag)
	stream, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect socket: %v\n", err)
		return 1
	}
	defer stream.Close()

	subscribe := protocol.Request{
		Action:  protocol.ActionSubscribe,
		Service: svc,
		Bot:     *bot,
		Target:  strings.TrimSpace(*target),
		Channel: strings.TrimSpace(*channel),
		Thread:  strings.TrimSpace(*thread),
	}
	if err := json.NewEncoder(stream).Encode(subscribe); err != nil {
		fmt.Fprintf(os.Stderr, "subscribe: %v\n", err)
		return 1
	}

	decoder := json.NewDecoder(stream)
	var ready protocol.Response
	if err := decoder.Decode(&ready); err != nil {
		fmt.Fprintf(os.Stderr, "subscribe: %v\n", err)
		return 1
	}
	if !ready.OK {
		fmt.Fprintln(os.Stderr, ready.Error)
		return 1
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var response protocol.Response
			if err := decoder.Decode(&response); err != nil {
				return
			}
			if !response.OK || response.Event == nil {
				continue
			}
			event := response.Event
			if event.Kind == "message" && event.Direction == "out" {
				fmt.Printf("\n%s> %s\n", *bot, event.Text)
				if isStdinTTY() {
					fmt.Printf("%s> ", *user)
				}
			}
		}
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	go func() {
		select {
		case <-interrupt:
			_ = stream.Close()
		case <-done:
		}
	}()

	if isStdinTTY() {
		fmt.Fprintln(os.Stdout, "Local chat ready. Type /quit to exit.")
		fmt.Printf("%s> ", *user)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			if isStdinTTY() {
				fmt.Printf("%s> ", *user)
			}
			continue
		}
		if text == "/quit" || text == "/exit" {
			break
		}

		resp, callErr := call(*socket, protocol.Request{
			Action:  protocol.ActionInject,
			Service: svc,
			Bot:     *bot,
			User:    *user,
			Target:  strings.TrimSpace(*target),
			Channel: strings.TrimSpace(*channel),
			Thread:  strings.TrimSpace(*thread),
			Text:    text,
		})
		if callErr != nil {
			fmt.Fprintln(os.Stderr, callErr)
			return 1
		}
		if !resp.OK {
			fmt.Fprintln(os.Stderr, resp.Error)
			return 1
		}
		if isStdinTTY() {
			fmt.Printf("%s> ", *user)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		return 1
	}

	return 0
}

func runBots(service string, args []string) int {
	flags := flag.NewFlagSet("bots", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "filter by service")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	resp, err := call(*socket, protocol.Request{Action: protocol.ActionBots, Service: svc})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(resp.Bots)
		return 0
	}

	for _, bot := range resp.Bots {
		fmt.Printf("%s\t%s\t%s\t%s\n", bot.Service, bot.Name, bot.BotID, bot.DisplayName)
	}

	return 0
}

func runStatus(service string, args []string) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	_ = service // status is global - no service filter

	resp, err := call(*socket, protocol.Request{Action: protocol.ActionStatus})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if resp.Status == nil {
		fmt.Fprintln(os.Stderr, "daemon returned empty status")
		return 1
	}

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(resp.Status)
		return 0
	}

	st := resp.Status
	fmt.Printf("uptime:  %s\n", formatUptime(st.UptimeSec))
	fmt.Printf("started: %s\n", st.StartedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Printf("bots:    %d\n", len(st.Bots))
	for _, b := range st.Bots {
		name := b.DisplayName
		if name == "" {
			name = b.Name
		}
		fmt.Printf("  %-20s  %s\n", name, b.Service)
		for _, binding := range b.Agents {
			label := binding.Agent
			if binding.Name != "" {
				label = binding.Name + " -> " + binding.Agent
			}
			fmt.Printf("    %-18s  when: %s\n", label, binding.When)
		}
	}
	fmt.Printf("agents:  %d\n", len(st.Agents))
	for _, a := range st.Agents {
		fmt.Printf("  %-20s  %s\n", a.Name, a.Driver)
	}
	if st.Notifications != nil {
		fmt.Printf("notifications: total=%d unseen=%d\n", st.Notifications.Total, st.Notifications.Unseen)
	}

	return 0
}

// formatUptime formats a duration in seconds as a human-readable string.
func formatUptime(secs int64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	return fmt.Sprintf("%dh%dm", h, m)
}

func runSend(service string, args []string) int {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "generic destination id (room/channel/user/thread root)")
	channel := flags.String("channel", "", "channel destination id")
	thread := flags.String("thread", "", "thread id")
	text := flags.String("text", "", "message text (use - to read from stdin)")
	format := flags.String("format", "plain", "message format (plain, markdown, html)")
	var attach stringList
	flags.Var(&attach, "attach", "local file path to upload (repeat for multiple files)")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	if strings.TrimSpace(*bot) == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}

	// Resolve message text: explicit flag, stdin sentinel (-), or implicit
	// stdin when the flag is omitted and stdin is not a terminal. With
	// attachments present, stdin is left alone unless explicitly requested -
	// `pantalk send --attach file.png` in a pipeline should not block waiting
	// for a caption that is never coming.
	messageText := *text
	if messageText == "-" || (messageText == "" && len(attach) == 0 && !isStdinTTY()) {
		stdinText, err := readStdin()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		messageText = stdinText
	}

	if strings.TrimSpace(messageText) == "" && len(attach) == 0 {
		fmt.Fprintln(os.Stderr, "--text is required (or pass message via stdin, or use --attach)")
		return 2
	}
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		fmt.Fprintln(os.Stderr, "one of --target, --channel, or --thread is required")
		return 2
	}

	// The daemon opens these paths in its own working directory, so resolve
	// them here while the user's cwd is still the right frame of reference.
	attachPaths, err := resolveAttachments(attach)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:  protocol.ActionSend,
		Service: svc,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
		Text:    messageText,
		Format:  *format,
		Attach:  attachPaths,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if resp.Event != nil {
		if *jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(resp.Event)
		} else {
			printEvent(*resp.Event)
		}
	}

	return 0
}

func runReact(service string, args []string) int {
	flags := flag.NewFlagSet("react", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "bot name from config")
	channel := flags.String("channel", "", "channel id containing the message")
	thread := flags.String("thread", "", "message timestamp / thread id (required for Slack)")
	target := flags.String("target", "", "message id (required for Discord)")
	emoji := flags.String("emoji", "", "emoji reaction to add (e.g. white_check_mark, 👍)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	if strings.TrimSpace(*bot) == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}
	if strings.TrimSpace(*emoji) == "" {
		fmt.Fprintln(os.Stderr, "--emoji is required")
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:  protocol.ActionReact,
		Service: svc,
		Bot:     *bot,
		Channel: *channel,
		Thread:  *thread,
		Target:  *target,
		Emoji:   *emoji,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	fmt.Println(resp.Ack)
	return 0
}

// runTyping starts (or stops) a daemon-managed typing lease. One call keeps
// the "bot is typing..." indicator alive until a send to the same destination,
// an explicit --stop, or the daemon-side timeout - the agent does not need to
// re-pulse while it thinks.
func runTyping(service string, args []string) int {
	flags := flag.NewFlagSet("typing", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "bot name from config")
	channel := flags.String("channel", "", "channel destination id")
	thread := flags.String("thread", "", "thread id")
	target := flags.String("target", "", "generic destination id")
	stop := flags.Bool("stop", false, "stop the typing indicator instead of starting it")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	if strings.TrimSpace(*bot) == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		fmt.Fprintln(os.Stderr, "one of --target, --channel, or --thread is required")
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:  protocol.ActionTyping,
		Service: svc,
		Bot:     *bot,
		Channel: *channel,
		Thread:  *thread,
		Target:  *target,
		Stop:    *stop,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	fmt.Println(resp.Ack)
	return 0
}

func runHistory(service string, args []string, forceNotify bool) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "filter by service")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "filter by destination id")
	channel := flags.String("channel", "", "filter by channel id")
	thread := flags.String("thread", "", "filter by thread id")
	search := flags.String("search", "", "filter messages containing this text (case-insensitive)")
	notify := flags.Bool("notify", forceNotify, "only return agent-relevant notification events")
	unseen := flags.Bool("unseen", false, "only return unseen notifications (notifications command)")
	limit := flags.Int("limit", 20, "number of events")
	sinceID := flags.Int64("since", 0, "only return events with id > since")
	clear := flags.Bool("clear", false, "delete matching events from the database")
	all := flags.Bool("all", false, "allow broad clear across all bots/channels")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	if *clear {
		return runClear(svc, *socket, *bot, *target, *channel, *thread, *search, *unseen, *all, forceNotify, *jsonOut)
	}

	resp, err := call(*socket, protocol.Request{
		Action:  toAction(forceNotify),
		Service: svc,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
		Search:  *search,
		Notify:  *notify,
		Unseen:  *unseen,
		Limit:   *limit,
		SinceID: *sinceID,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(resp.Events)
		return 0
	}

	for _, event := range resp.Events {
		printEvent(event)
	}

	return 0
}

func runSubscribe(service string, args []string) int {
	flags := flag.NewFlagSet("stream", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "filter by service")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "filter by destination id")
	channel := flags.String("channel", "", "filter by channel id")
	thread := flags.String("thread", "", "filter by thread id")
	search := flags.String("search", "", "filter messages containing this text (case-insensitive)")
	notify := flags.Bool("notify", false, "only stream agent-relevant notification events")
	timeoutSec := flags.Int("timeout", 60, "disconnect after N seconds (0 = no timeout)")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	conn, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect socket: %v\n", err)
		return 1
	}
	defer conn.Close()

	// Set a hard deadline on the connection so agent tools never block
	// indefinitely. A timeout of 0 disables the deadline for interactive use.
	if *timeoutSec > 0 {
		_ = conn.SetDeadline(time.Now().Add(time.Duration(*timeoutSec) * time.Second))
	}

	request := protocol.Request{
		Action:  protocol.ActionSubscribe,
		Service: svc,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
		Search:  *search,
		Notify:  *notify,
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		fmt.Fprintf(os.Stderr, "send request: %v\n", err)
		return 1
	}

	decoder := json.NewDecoder(conn)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)

	go func() {
		<-interrupt
		_ = conn.Close()
	}()

	for {
		var resp protocol.Response
		if err := decoder.Decode(&resp); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return 0
			}
			// Deadline exceeded is a normal exit for timed streams.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return 0
			}
			fmt.Fprintln(os.Stderr, err)
			return 0
		}

		if !resp.OK {
			fmt.Fprintln(os.Stderr, resp.Error)
			return 1
		}

		if resp.Event == nil {
			continue
		}

		if *jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(resp.Event)
			continue
		}

		printEvent(*resp.Event)
	}
}

func runPing(args []string) int {
	flags := flag.NewFlagSet("ping", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	resp, err := call(*socket, protocol.Request{Action: protocol.ActionPing})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	fmt.Println(resp.Ack)
	return 0
}

func runClear(service string, socket string, bot string, target string, channel string, thread string, search string, unseen bool, all bool, forceNotify bool, jsonOut bool) int {
	if !all && strings.TrimSpace(bot) == "" && strings.TrimSpace(target) == "" && strings.TrimSpace(channel) == "" && strings.TrimSpace(thread) == "" {
		fmt.Fprintln(os.Stderr, "refusing broad clear without scope: provide filters or --all")
		return 2
	}

	action := protocol.ActionClearHistory
	if forceNotify {
		action = protocol.ActionClearNotify
	}

	resp, err := call(socket, protocol.Request{
		Action:  action,
		Service: service,
		Bot:     bot,
		Target:  target,
		Channel: channel,
		Thread:  thread,
		Search:  search,
		Unseen:  unseen,
		All:     all,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !resp.OK {
		fmt.Fprintln(os.Stderr, resp.Error)
		return 1
	}

	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(resp)
		return 0
	}

	fmt.Printf("cleared=%d\n", resp.Cleared)
	return 0
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

// stringList collects a repeatable string flag, so --attach can be passed more
// than once in a single send.
type stringList []string

func (s *stringList) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("value cannot be empty")
	}
	*s = append(*s, trimmed)
	return nil
}

// resolveAttachments converts attachment paths to absolute form and checks that
// each one is a readable regular file. Failing here gives the user an error
// naming the path they typed, rather than a daemon-side error about a path they
// never wrote.
func resolveAttachments(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	resolved := make([]string, 0, len(paths))
	for _, raw := range paths {
		absolute, err := filepath.Abs(raw)
		if err != nil {
			return nil, fmt.Errorf("resolve attachment %q: %w", raw, err)
		}

		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", raw, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("attachment %q is a directory", raw)
		}

		resolved = append(resolved, absolute)
	}

	return resolved, nil
}

func printEvent(event protocol.Event) {
	fmt.Printf("%d\tnid=%d\tseen=%t\t%s\t%s/%s\t%s\t%s\tuser=%s self=%t\tnotify=%t direct=%t mention=%t\ttarget=%s channel=%s thread=%s\t%s\n",
		event.ID,
		event.NotificationID,
		event.Seen,
		event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
		event.User,
		event.Self,
		event.Notify,
		event.Direct,
		event.Mentions,
		event.Target,
		event.Channel,
		event.Thread,
		event.Text,
	)

	// Attachments print on their own indented lines so the tab-delimited event
	// line above stays parseable by cut/awk.
	for _, attachment := range event.Attachments {
		fmt.Printf("\tattachment\tname=%s\tmime=%s\tsize=%d\tpath=%s\n",
			attachment.Name,
			attachment.MIME,
			attachment.Size,
			attachment.Path,
		)
	}
}

func toAction(notifications bool) string {
	if notifications {
		return protocol.ActionNotify
	}
	return protocol.ActionHistory
}

// resolveService returns the service to use for a request. The --service flag
// value is used when provided; otherwise the service is auto-resolved from the
// bot name by the daemon.
func resolveService(binaryService string, flagService string) string {
	if binaryService != "" {
		return binaryService
	}
	return flagService
}

func printUsage(toolName string) {
	svcHint := ""
	if toolName == "pantalk" {
		svcHint = " [--service NAME]"
	}

	fmt.Fprintf(os.Stderr, `%s - unified CLI for pantalk

Messaging:
  %s local [--driver codex|claude] [--workdir PATH] [--ephemeral]
  %s bots%s [--json]
  %s status [--json]
  %s send --bot NAME (--text MESSAGE | --text - | --attach PATH) (--target ID | --channel ID | --thread ID) [--attach PATH]... [--format plain|markdown|html]%s [--json]
  %s inject --bot NAME --user ID --text MESSAGE [--target ID | --channel ID | --thread ID]%s [--json]
  %s chat --bot NAME [--user ID] [--target ID | --channel ID | --thread ID]%s
  %s react --bot NAME --emoji EMOJI (--channel ID | --thread ID | --target ID)%s
  %s typing --bot NAME (--channel ID | --thread ID | --target ID) [--stop]%s
  %s history [--bot NAME] [--channel ID] [--thread ID] [--search TEXT] [--notify] [--limit N] [--since ID] [--clear [--all]]%s [--json]
  %s notifications [--bot NAME] [--channel ID] [--thread ID] [--search TEXT] [--unseen] [--limit N] [--since ID] [--clear [--all]]%s [--json]
  %s stream [--bot NAME] [--channel ID] [--thread ID] [--search TEXT] [--notify] [--timeout N]%s [--json]
  %s ping

Skills:
  %s skill install [--scope project|user|all] [--agents ...] [--repo URL] [--dry-run]
  %s skill update  [--scope project|user|all] [--agents ...]
  %s skill list

Admin:
  %s setup [--output PATH] [--force]
  %s validate [--config PATH]
  %s reload [--socket PATH]
  %s pair --bot NAME [--config PATH]
  %s config print [--config PATH]
  %s config list-bots [--config PATH] [--json]
  %s config set-server [--socket ...] [--db ...] [--history ...]
  %s config add-bot --name NAME --type TYPE [--bot-token ...] [--app-level-token ...] [--endpoint ...] [--transport ...] [--channels ...]
  %s config remove-bot --name NAME

JSON output is enabled by default when stdout is not a terminal.
`, toolName,
		toolName,
		toolName, svcHint,
		toolName,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName, svcHint,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName,
		toolName)
}
