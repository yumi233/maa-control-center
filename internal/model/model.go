package model

import "encoding/json"

type Policy struct {
	MedicineBudget *int   `json:"medicine_budget"`
	StoneBudget    *int   `json:"stone_budget"`
	ExpiringHours  *int   `json:"expiring_hours"`
	Source         string `json:"source"`
	Verified       bool   `json:"verified"`
}

type Profile struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Policy       Policy          `json:"policy"`
	Tasks        []string        `json:"tasks"`
	AccountHint  string          `json:"account_hint"`
	Schedule     json.RawMessage `json:"schedule,omitempty"`
	ScheduleHash string          `json:"schedule_hash"`
	Inventory    []InventoryItem `json:"inventory"`
	Editable     json.RawMessage `json:"editable,omitempty"`
}

type InventoryItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Current    *int   `json:"current"`
	Minimum    *int   `json:"minimum"`
	Target     *int   `json:"target"`
	Upper      *int   `json:"upper"`
	Priority   int    `json:"priority"`
	Stage      string `json:"stage"`
	Source     string `json:"source"`
	ObservedAt string `json:"observed_at"`
}

type NativeTimer struct {
	ID        int    `json:"id"`
	ProfileID string `json:"profile_id"`
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	Enabled   bool   `json:"enabled"`
}

type Instance struct {
	TaskExecution      bool            `json:"task_execution"`
	CurrentProfile     string          `json:"current_profile"`
	NativeTimers       []NativeTimer   `json:"native_timers"`
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Version            string          `json:"version"`
	ProcessRunning     bool            `json:"process_running"`
	ProcessVerified    bool            `json:"process_verified"`
	ConfigurationWrite bool            `json:"configuration_write"`
	State              string          `json:"state"`
	DeviceConnected    *bool           `json:"device_connected"`
	Fingerprint        string          `json:"fingerprint"`
	Profiles           []Profile       `json:"profiles"`
	Schedule           json.RawMessage `json:"schedule,omitempty"`
	Logs               []string        `json:"logs"`
}

type Heartbeat struct {
	AgentID     string       `json:"agent_id"`
	Hostname    string       `json:"hostname"`
	OS          string       `json:"os"`
	Version     string       `json:"version"`
	Instances   []Instance   `json:"instances"`
	Completions []Completion `json:"completions,omitempty"`
}

type Completion struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	State      string `json:"state"`
	ObservedAt string `json:"observed_at"`
	Source     string `json:"source"`
}

type SanityReading struct {
	Current    int    `json:"current"`
	Maximum    int    `json:"maximum"`
	ObservedAt string `json:"observed_at"`
}
type StageActivity struct {
	Stage       string `json:"stage"`
	Settlements int    `json:"settlements"`
}
type ActivityDrop struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}
type RunActivity struct {
	StartedAt    string          `json:"started_at"`
	FinishedAt   string          `json:"finished_at"`
	Tasks        []string        `json:"tasks"`
	Stages       []StageActivity `json:"stages"`
	Drops        []ActivityDrop  `json:"drops"`
	FirstSanity  *SanityReading  `json:"first_sanity,omitempty"`
	Sanity       *SanityReading  `json:"sanity,omitempty"`
	LastBattleAt string          `json:"last_battle_at,omitempty"`
	LogsComplete bool            `json:"logs_complete"`
	Issues       []string        `json:"issues,omitempty"`
}

type Binding struct {
	AccountID  string `json:"account_id"`
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	ProfileID  string `json:"profile_id"`
}

type Account struct {
	OwnerID     string    `json:"owner_id"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	Bound       bool      `json:"bound"`
	AgentOnline bool      `json:"agent_online"`
	LastSeen    string    `json:"last_seen"`
	Instance    *Instance `json:"instance"`
	Profile     *Profile  `json:"profile"`
	Policy      Policy    `json:"policy"`
}

type Event struct {
	ID        int64  `json:"id"`
	Time      string `json:"time"`
	Level     string `json:"level"`
	AccountID string `json:"account_id"`
	Message   string `json:"message"`
}
