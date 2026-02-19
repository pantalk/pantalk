package client

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pantalk/pantalk/internal/config"
	"github.com/pantalk/pantalk/internal/ctl"
	"github.com/pantalk/pantalk/internal/protocol"
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
	case "send":
		return runSend(service, commandArgs)
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

func runBots(service string, args []string) int {
	flags := flag.NewFlagSet("bots", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "filter by service (slack, discord, mattermost, telegram, whatsapp)")
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

func runSend(service string, args []string) int {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "service name (auto-resolved from bot if omitted)")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "generic destination id (room/channel/user/thread root)")
	channel := flags.String("channel", "", "channel destination id")
	thread := flags.String("thread", "", "thread id")
	text := flags.String("text", "", "message text")
	jsonOut := flags.Bool("json", !isTTY(), "output as JSON (default when stdout is not a terminal)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	svc := resolveService(service, *svcFlag)

	if strings.TrimSpace(*bot) == "" {
		fmt.Fprintln(os.Stderr, "--bot is required")
		return 2
	}
	if strings.TrimSpace(*text) == "" {
		fmt.Fprintln(os.Stderr, "--text is required")
		return 2
	}
	if strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		fmt.Fprintln(os.Stderr, "one of --target, --channel, or --thread is required")
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:  protocol.ActionSend,
		Service: svc,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
		Text:    *text,
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

func runHistory(service string, args []string, forceNotify bool) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	svcFlag := flags.String("service", "", "filter by service (slack, discord, mattermost, telegram, whatsapp)")
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
	svcFlag := flags.String("service", "", "filter by service (slack, discord, mattermost, telegram, whatsapp)")
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
  %s bots%s [--json]
  %s send --bot NAME --text MESSAGE (--target ID | --channel ID | --thread ID)%s [--json]
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
  %s config set-server [--socket ...] [--db ...] [--history ...]
  %s config add-bot --name NAME --type TYPE [--bot-token ...] [--app-level-token ...] [--endpoint ...] [--transport ...] [--channels ...]
  %s config remove-bot --name NAME

JSON output is enabled by default when stdout is not a terminal.
`, toolName,
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
		toolName)
}
