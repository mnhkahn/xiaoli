package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Runner struct {
	registry *Registry
	agent    Agent
	now      func() time.Time
	newID    func() string
}

type RunnerConfig struct {
	Registry *Registry
	Agent    Agent
	Now      func() time.Time
	NewID    func() string
}

func NewRunner(cfg RunnerConfig) *Runner {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newID := cfg.NewID
	if newID == nil {
		newID = func() string { return fmt.Sprintf("run-%d", now().UnixNano()) }
	}
	return &Runner{registry: cfg.Registry, agent: cfg.Agent, now: now, newID: newID}
}

func (r *Runner) Run(ctx context.Context, workflowID string, input Input) (Run, error) {
	started := r.now()
	run := Run{
		ID:         r.newID(),
		WorkflowID: workflowID,
		Status:     RunFailed,
		Input:      input,
		StartedAt:  started,
	}
	if r == nil || r.registry == nil {
		run.Error = "workflow registry is not configured"
		run.FinishedAt = r.finishTime()
		return run, errors.New(run.Error)
	}
	if r.agent == nil {
		run.Error = "workflow agent is not configured"
		run.FinishedAt = r.finishTime()
		return run, errors.New(run.Error)
	}
	def, ok := r.registry.Get(workflowID)
	if !ok {
		run.Error = fmt.Sprintf("workflow %q not found", workflowID)
		run.FinishedAt = r.finishTime()
		return run, errors.New(run.Error)
	}
	if !def.Enabled {
		run.Error = fmt.Sprintf("workflow %q is disabled", workflowID)
		run.FinishedAt = r.finishTime()
		return run, errors.New(run.Error)
	}
	maxSteps := def.Agent.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	timeout := def.Agent.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lastErr := ""
	for attempt := 1; attempt <= maxSteps; attempt++ {
		stepStart := r.now()
		resp, err := r.agent.Run(runCtx, AgentRequest{
			Workflow:  def,
			Input:     input,
			Attempt:   attempt,
			MaxSteps:  maxSteps,
			LastError: lastErr,
		})
		step := Step{Index: len(run.Steps) + 1, Attempt: attempt, StartedAt: stepStart, Elapsed: r.now().Sub(stepStart)}
		if err != nil {
			lastErr = err.Error()
			step.Status = StepFailed
			step.Error = lastErr
			run.Steps = append(run.Steps, step)
			if runCtx.Err() != nil {
				break
			}
			continue
		}
		step.Status = StepSucceeded
		step.Text = strings.TrimSpace(resp.Text)
		run.Steps = append(run.Steps, step)
		run.Output = resp
		run.Status = RunSucceeded
		run.FinishedAt = r.now()
		return run, nil
	}
	if lastErr == "" && runCtx.Err() != nil {
		lastErr = runCtx.Err().Error()
	}
	if lastErr == "" {
		lastErr = "workflow exhausted without response"
	}
	run.Error = lastErr
	run.FinishedAt = r.now()
	return run, errors.New(lastErr)
}

func (r *Runner) finishTime() time.Time {
	if r == nil || r.now == nil {
		return time.Now()
	}
	return r.now()
}
