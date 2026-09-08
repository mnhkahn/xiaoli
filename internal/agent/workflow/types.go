package workflow

import (
	"context"
	"errors"
	"time"
)

type TriggerKind string

const (
	TriggerMessage TriggerKind = "message"
	TriggerCron    TriggerKind = "cron"
	TriggerManual  TriggerKind = "manual"
)

type Trigger struct {
	Kind TriggerKind
	Cron *CronSpec
}

type CronSpec struct {
	Every     time.Duration
	Timezone  string
	StartHour int
	EndHour   int
	AtHour    *int
	AtMinute  *int
	At        *time.Time // 一次性：到该绝对时刻触发一次
}

type AgentSpec struct {
	Name     string
	Mode     string
	MaxSteps int
	Timeout  time.Duration
}

type Definition struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	Action      string // speak | agent | notify（空则按 ID 走兼容逻辑）
	Channel     string // 来源 channel，用于发送时优先选择
	SenderID    string // 发送者 ID：飞书 open_id / ESP32 device_id
	Trigger     Trigger
	Agent       AgentSpec
	Metadata    map[string]any
}

type Input struct {
	Trigger        TriggerKind
	Channel        string
	ConversationID string
	DeviceID       string
	Text           string
	UseDeviceTools bool
	ScheduledAt    time.Time
	Metadata       map[string]any
}

type AgentRequest struct {
	Workflow  Definition
	Input     Input
	Attempt   int
	MaxSteps  int
	LastError string
}

type AgentResponse struct {
	Text     string
	Metadata map[string]any
	Finished bool
}

type Agent interface {
	Run(ctx context.Context, request AgentRequest) (AgentResponse, error)
}

// TerminalError marks an error for which a workflow must not schedule another
// step. It is used for configuration and authentication failures that cannot
// be resolved by asking the same agent to try again.
type TerminalError struct {
	Err error
}

func (e *TerminalError) Error() string {
	if e == nil || e.Err == nil {
		return "terminal workflow error"
	}
	return e.Err.Error()
}

func (e *TerminalError) Unwrap() error { return e.Err }

func NewTerminalError(err error) error {
	if err == nil {
		return nil
	}
	return &TerminalError{Err: err}
}

func IsTerminalError(err error) bool {
	var terminal *TerminalError
	return errors.As(err, &terminal)
}

type RunStatus string

const (
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

type StepStatus string

const (
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
)

type Step struct {
	Index     int           `json:"index"`
	Attempt   int           `json:"attempt"`
	Status    StepStatus    `json:"status"`
	Error     string        `json:"error,omitempty"`
	Text      string        `json:"text,omitempty"`
	Elapsed   time.Duration `json:"elapsed"`
	StartedAt time.Time     `json:"started_at"`
}

type Run struct {
	ID         string        `json:"id"`
	WorkflowID string        `json:"workflow_id"`
	Status     RunStatus     `json:"status"`
	Input      Input         `json:"input"`
	Output     AgentResponse `json:"output"`
	Steps      []Step        `json:"steps"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}
