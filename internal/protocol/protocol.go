package protocol

import "time"

const (
	ActionPing         = "ping"
	ActionBots         = "bots"
	ActionStatus       = "status"
	ActionSend         = "send"
	ActionHistory      = "history"
	ActionNotify       = "notifications"
	ActionClearHistory = "clear_history"
	ActionClearNotify  = "clear_notifications"
	ActionSubscribe    = "subscribe"
	ActionReload       = "reload"
)

type Request struct {
	Action  string `json:"action"`
	Service string `json:"service,omitempty"`
	Bot     string `json:"bot,omitempty"`
	Target  string `json:"target,omitempty"`
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Text    string `json:"text,omitempty"`
	Search  string `json:"search,omitempty"`
	Notify  bool   `json:"notify,omitempty"`
	Unseen  bool   `json:"unseen,omitempty"`
	All     bool   `json:"all,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	SinceID int64  `json:"since_id,omitempty"`
}

type Response struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Ack     string       `json:"ack,omitempty"`
	Bots    []BotRef     `json:"bots,omitempty"`
	Events  []Event      `json:"events,omitempty"`
	Event   *Event       `json:"event,omitempty"`
	Cleared int64        `json:"cleared,omitempty"`
	Status  *DaemonStatus `json:"status,omitempty"`
}

// DaemonStatus holds a snapshot of the daemon's runtime state returned by
// the "status" action. It is designed to be consumed by agents and operators
// who need to quickly verify that pantalkd is healthy.
type DaemonStatus struct {
	StartedAt time.Time   `json:"started_at"`
	UptimeSec int64       `json:"uptime_sec"`
	Bots      []BotStatus `json:"bots"`
	Agents    []AgentInfo `json:"agents"`
}

// BotStatus describes a single configured bot.
type BotStatus struct {
	Name        string `json:"name"`
	Service     string `json:"service"`
	DisplayName string `json:"display_name,omitempty"`
}

// AgentInfo describes a configured agent runner.
type AgentInfo struct {
	Name string `json:"name"`
	When string `json:"when"`
}

type BotRef struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	BotID       string `json:"bot_id"`
	DisplayName string `json:"display_name,omitempty"`
}

type Event struct {
	ID             int64      `json:"id"`
	Timestamp      time.Time  `json:"timestamp"`
	Service        string     `json:"service"`
	Bot            string     `json:"bot"`
	Kind           string     `json:"kind"`
	Direction      string     `json:"direction"`
	User           string     `json:"user,omitempty"`
	Self           bool       `json:"self,omitempty"`
	Target         string     `json:"target,omitempty"`
	Channel        string     `json:"channel,omitempty"`
	Thread         string     `json:"thread,omitempty"`
	NotificationID int64      `json:"notification_id,omitempty"`
	Seen           bool       `json:"seen,omitempty"`
	SeenAt         *time.Time `json:"seen_at,omitempty"`
	Mentions       bool       `json:"mentions_agent,omitempty"`
	Direct         bool       `json:"direct_to_agent,omitempty"`
	Notify         bool       `json:"notify,omitempty"`
	Text           string     `json:"text"`
}
