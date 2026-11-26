package model

import "time"

// Event types returned by the ntfy JSON stream.
const (
	EventOpen        = "open"
	EventKeepalive   = "keepalive"
	EventMessage     = "message"
	EventMessageDel  = "message_delete"
	EventMessageClr  = "message_clear"
	EventPollRequest = "poll_request"
)

// Message is one line of the ntfy JSON stream. Only the fields we use are
// modelled; unknown fields are ignored by encoding/json.
type Message struct {
	ID         string      `json:"id"`
	Time       int64       `json:"time"`
	Event      string      `json:"event"`
	Topic      string      `json:"topic"`
	Message    string      `json:"message"`
	Title      string      `json:"title"`
	Priority   int         `json:"priority"`
	Tags       []string    `json:"tags"`
	Click      string      `json:"click"`
	Icon       string      `json:"icon"`
	Actions    []RawAction `json:"actions"`
	Attachment *Attachment `json:"attachment"`
	Sender     string      `json:"sender"`
	// Raw is the original JSON line, available to scripts as m.raw.
	Raw string `json:"-"`
}

// RawAction mirrors the ntfy "actions" field. Only "view" actions are used by
// the default mapping; Lua scripts see the full structure.
type RawAction struct {
	ID      string            `json:"id"`
	Action  string            `json:"action"`
	Label   string            `json:"label"`
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Clear   bool              `json:"clear"`
}

// Attachment mirrors the ntfy attachment object.
type Attachment struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Expires int64  `json:"expires"`
}

// ActionType is the kind of local side effect a toast click can trigger.
type ActionType string

const (
	ActionCopy ActionType = "copy"
	ActionOpen ActionType = "open"
	ActionHTTP ActionType = "http"
	ActionRun  ActionType = "run"
)

// Action is a declarative side effect attached to a toast body or button.
// It is validated by the action executor before it is written to disk.
type Action struct {
	Type      ActionType        `json:"type"`
	Text      string            `json:"text,omitempty"`
	Sensitive bool              `json:"sensitive,omitempty"`
	URL       string            `json:"url,omitempty"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	Timeout   time.Duration     `json:"timeout,omitempty"`
	Cmd       string            `json:"cmd,omitempty"`
	Args      []string          `json:"args,omitempty"`
}

// Button is a toast button with its action.
type Button struct {
	Label  string
	Action Action
}

// Spec is what a Lua script returns: a complete toast description. A nil Spec
// means "do not notify".
type Spec struct {
	Title    string
	Body     string
	Priority int
	Sound    string // default | mail | silent
	Duration string // short | long
	Icon     string
	Click    string // URL opened when the body is clicked
	OnClick  *Action
	Buttons  []Button
}

// Notification is the platform-neutral toast payload handed to a Notifier.
type Notification struct {
	Title    string
	Body     string
	Sound    string // default | mail | silent
	Duration string // short | long
	Icon     string
	Launch   string // protocol URI for body click, may be empty
	Actions  []ToastAction
}

// ToastAction is a toast button that opens a protocol URI.
type ToastAction struct {
	Label  string
	Launch string
}
