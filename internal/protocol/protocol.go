package protocol

import "time"

const (
	ActionPing        = "ping"
	ActionBots        = "bots"
	ActionSend        = "send"
	ActionHistory     = "history"
	ActionNotify      = "notifications"
	ActionClearNotify = "clear_notifications"
	ActionSubscribe   = "subscribe"
	ActionReload      = "reload"
)

type Request struct {
	Action         string `json:"action"`
	Service        string `json:"service,omitempty"`
	Bot            string `json:"bot,omitempty"`
	Target         string `json:"target,omitempty"`
	Channel        string `json:"channel,omitempty"`
	Thread         string `json:"thread,omitempty"`
	Text           string `json:"text,omitempty"`
	Notify         bool   `json:"notify,omitempty"`
	Unseen         bool   `json:"unseen,omitempty"`
	All            bool   `json:"all,omitempty"`
	NotificationID int64  `json:"notification_id,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	SinceID        int64  `json:"since_id,omitempty"`
}

type Response struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Ack     string   `json:"ack,omitempty"`
	Bots    []BotRef `json:"bots,omitempty"`
	Events  []Event  `json:"events,omitempty"`
	Event   *Event   `json:"event,omitempty"`
	Cleared int64    `json:"cleared,omitempty"`
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
