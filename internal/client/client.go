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

	"github.com/chatbotkit/pantalk/internal/protocol"
)

const defaultSocketPath = "/tmp/pantalk.sock"

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
	case "clear-notifications", "clear-notify", "ack":
		return runClearNotifications(service, commandArgs)
	case "stream", "subscribe":
		return runSubscribe(service, commandArgs)
	case "ping":
		return runPing(commandArgs)
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
	jsonOut := flags.Bool("json", false, "print JSON response")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	resp, err := call(*socket, protocol.Request{Action: protocol.ActionBots, Service: service})
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
		fmt.Printf("%s\t%s\t%s\n", bot.Name, bot.BotID, bot.DisplayName)
	}

	return 0
}

func runSend(service string, args []string) int {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "generic destination id (room/channel/user/thread root)")
	channel := flags.String("channel", "", "channel destination id")
	thread := flags.String("thread", "", "thread id")
	text := flags.String("text", "", "message text")
	if err := flags.Parse(args); err != nil {
		return 2
	}

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
		Service: service,
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
		printEvent(*resp.Event)
	}

	return 0
}

func runHistory(service string, args []string, forceNotify bool) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "filter by destination id")
	channel := flags.String("channel", "", "filter by channel id")
	thread := flags.String("thread", "", "filter by thread id")
	notify := flags.Bool("notify", forceNotify, "only return agent-relevant notification events")
	unseen := flags.Bool("unseen", false, "only return unseen notifications (notifications command)")
	limit := flags.Int("limit", 20, "number of events")
	sinceID := flags.Int64("since", 0, "only return events with id > since")
	jsonOut := flags.Bool("json", false, "print JSON response")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:  toAction(forceNotify),
		Service: service,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
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
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "filter by destination id")
	channel := flags.String("channel", "", "filter by channel id")
	thread := flags.String("thread", "", "filter by thread id")
	notify := flags.Bool("notify", false, "only stream agent-relevant notification events")
	jsonOut := flags.Bool("json", false, "print JSON response")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	conn, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect socket: %v\n", err)
		return 1
	}
	defer conn.Close()

	request := protocol.Request{
		Action:  protocol.ActionSubscribe,
		Service: service,
		Bot:     *bot,
		Target:  *target,
		Channel: *channel,
		Thread:  *thread,
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
			if !errors.Is(err, net.ErrClosed) {
				fmt.Fprintln(os.Stderr, err)
			}
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

func runClearNotifications(service string, args []string) int {
	flags := flag.NewFlagSet("clear-notifications", flag.ContinueOnError)
	socket := flags.String("socket", defaultSocketPath, "unix socket path")
	bot := flags.String("bot", "", "bot name from config")
	target := flags.String("target", "", "clear by destination id")
	channel := flags.String("channel", "", "clear by channel id")
	thread := flags.String("thread", "", "clear by thread id")
	id := flags.Int64("id", 0, "clear a single notification by notification id")
	all := flags.Bool("all", false, "allow broad clear across scope")
	unseenOnly := flags.Bool("unseen", true, "clear only unseen notifications")
	jsonOut := flags.Bool("json", false, "print JSON response")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *id <= 0 && !*all && strings.TrimSpace(*bot) == "" && strings.TrimSpace(*target) == "" && strings.TrimSpace(*channel) == "" && strings.TrimSpace(*thread) == "" {
		fmt.Fprintln(os.Stderr, "refusing broad clear without scope: provide --id, filters, or --all")
		return 2
	}

	resp, err := call(*socket, protocol.Request{
		Action:         protocol.ActionClearNotify,
		Service:        service,
		Bot:            *bot,
		Target:         *target,
		Channel:        *channel,
		Thread:         *thread,
		Unseen:         *unseenOnly,
		All:            *all,
		NotificationID: *id,
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
	fmt.Printf("%d\tnid=%d\tseen=%t\t%s\t%s/%s\t%s\t%s\tnotify=%t direct=%t mention=%t\ttarget=%s channel=%s thread=%s\t%s\n",
		event.ID,
		event.NotificationID,
		event.Seen,
		event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		event.Service,
		event.Bot,
		event.Kind,
		event.Direction,
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

func printUsage(toolName string) {
	fmt.Fprintf(os.Stderr, `%s - service client for pantalkd

Usage:
  %s bots [--socket path] [--json]
	%s send --bot NAME --text MESSAGE (--target ID | --channel ID | --thread ID) [--socket path]
	%s history [--bot NAME] [--target ID] [--channel ID] [--thread ID] [--notify] [--limit N] [--since ID] [--socket path] [--json]
	%s notifications [--bot NAME] [--target ID] [--channel ID] [--thread ID] [--unseen] [--limit N] [--since ID] [--socket path] [--json]
	%s clear-notifications [--id N | --bot NAME | --target ID | --channel ID | --thread ID | --all] [--unseen] [--socket path] [--json]
	%s stream [--bot NAME] [--target ID] [--channel ID] [--thread ID] [--notify] [--socket path] [--json]
  %s ping [--socket path]
`, toolName, toolName, toolName, toolName, toolName, toolName, toolName, toolName)
}
