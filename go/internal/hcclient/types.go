// Package hcclient implements the published HC generation-one JSON-RPC API.
package hcclient

import "encoding/json"

type CoreInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type HelloResult struct {
	Generation uint32   `json:"generation"`
	Version    string   `json:"version"`
	Core       CoreInfo `json:"core"`
	Framed     bool     `json:"framed"`
	Methods    []string `json:"methods"`
	Features   []string `json:"features"`
}
type Status struct {
	State    string  `json:"state"`
	App      string  `json:"app,omitempty"`
	Msg      string  `json:"msg,omitempty"`
	Title    string  `json:"title,omitempty"`
	Kind     *string `json:"kind,omitempty"`
	Progress *uint8  `json:"progress,omitempty"`
}
type Foreground struct {
	PID  int32    `json:"pid"`
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}
type Terminal struct {
	Address       string            `json:"address"`
	Labels        map[string]string `json:"labels"`
	Argv          []string          `json:"argv"`
	Cwd           string            `json:"cwd"`
	State         string            `json:"state"`
	CreatedMS     uint64            `json:"created_ms"`
	Screen        string            `json:"screen"`
	Cols          uint16            `json:"cols"`
	Rows          uint16            `json:"rows"`
	Name          *string           `json:"name,omitempty"`
	Status        *Status           `json:"status,omitempty"`
	StatusMS      *uint64           `json:"status_ms,omitempty"`
	Turn          uint64            `json:"turn"`
	PID           *uint32           `json:"pid,omitempty"`
	Foreground    *Foreground       `json:"foreground,omitempty"`
	Reason        *string           `json:"reason,omitempty"`
	ExitCode      *int32            `json:"exit_code,omitempty"`
	Signal        *int32            `json:"signal,omitempty"`
	FinishedMS    *uint64           `json:"finished_ms,omitempty"`
	Parent        *string           `json:"parent,omitempty"`
	Template      *string           `json:"template,omitempty"`
	Submit        *string           `json:"submit,omitempty"`
	Stop          *string           `json:"stop,omitempty"`
	PasteGapMS    *uint64           `json:"paste_gap_ms,omitempty"`
	Keep          bool              `json:"keep"`
	RM            bool              `json:"rm"`
	Notify        bool              `json:"notify"`
	DieWithParent bool              `json:"die_with_parent"`
	Reported      bool              `json:"reported"`
	Records       map[string]Status `json:"records,omitempty"`
	Title         string            `json:"title"`
	Identity      string            `json:"identity"`
}
type FirstMessage struct {
	Started *bool   `json:"started,omitempty"`
	Turn    *uint64 `json:"turn,omitempty"`
	Sent    bool    `json:"sent"`
	Reason  *string `json:"reason,omitempty"`
}
type RunResult struct {
	Terminal
	FirstMessage *FirstMessage `json:"first_message,omitempty"`
	Wait         *WaitResult   `json:"wait,omitempty"`
	Output       *string       `json:"output,omitempty"`
}
type RunParams struct {
	Argv          []string          `json:"argv,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Name          string            `json:"name,omitempty"`
	Parent        string            `json:"parent,omitempty"`
	Caller        string            `json:"caller,omitempty"`
	NoParent      bool              `json:"no_parent,omitempty"`
	OutliveParent bool              `json:"outlive_parent"`
	NoNotify      bool              `json:"no_notify"`
	Keep          bool              `json:"keep"`
	RM            bool              `json:"rm"`
	Wait          bool              `json:"wait,omitempty"`
	Timeout       *float64          `json:"timeout,omitempty"`
}

// SendParams intentionally exposes no force or raw key bypass.
// An omitted If is reserved by Sum's caller for a qualified Done queue path.
type SendParams struct {
	Terminal string   `json:"terminal"`
	Text     string   `json:"text"`
	If       string   `json:"if,omitempty"`
	Caller   string   `json:"caller,omitempty"`
	Wait     bool     `json:"wait,omitempty"`
	Timeout  *float64 `json:"timeout,omitempty"`
}
type SendResult struct {
	Turn    uint64      `json:"turn"`
	Status  *string     `json:"status,omitempty"`
	Started *bool       `json:"started,omitempty"`
	Wait    *WaitResult `json:"wait,omitempty"`
}
type ListParams struct {
	Labels map[string]string `json:"labels,omitempty"`
	All    bool              `json:"all,omitempty"`
}
type ListResult struct {
	Terminals      []Terminal `json:"terminals"`
	HiddenFinished uint64     `json:"hidden_finished"`
}
type ReadResult struct {
	Text     string  `json:"text"`
	Complete bool    `json:"complete"`
	How      string  `json:"how"`
	Why      *string `json:"why,omitempty"`
}
type WaitParams struct {
	Terminal string   `json:"terminal"`
	Caller   string   `json:"caller,omitempty"`
	Timeout  *float64 `json:"timeout,omitempty"`
	Output   string   `json:"output,omitempty"`
	Children string   `json:"children,omitempty"`
}
type WaitResult struct {
	Terminal string       `json:"terminal"`
	Ended    bool         `json:"ended"`
	Reason   string       `json:"reason"`
	Turn     uint64       `json:"turn"`
	Children []WaitResult `json:"children"`
	Status   *string      `json:"status,omitempty"`
	ExitCode *int32       `json:"exit_code,omitempty"`
	Signal   *int32       `json:"signal,omitempty"`
	From     *string      `json:"from,omitempty"`
}
type SubscribeParams struct {
	Labels     map[string]string `json:"labels,omitempty"`
	Terminal   string            `json:"terminal,omitempty"`
	ChildrenOf string            `json:"children_of,omitempty"`
}
type SubscribeResult struct {
	Seq       uint64     `json:"seq"`
	Terminals []Terminal `json:"terminals"`
	Channel   *uint32    `json:"channel,omitempty"`
}

// Notification retains the native payload, including event-specific fields.
type Notification struct {
	Method string
	Params json.RawMessage
}

type InterruptResult struct {
	Outcome string `json:"outcome"`
	Key     string `json:"key"`
}
type RemoveResult struct {
	Removed string `json:"removed"`
}
