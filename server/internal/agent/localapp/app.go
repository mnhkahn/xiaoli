package localapp

import (
	"fmt"

	"xiaoli/server/internal/agent/localconfig"
	"xiaoli/server/internal/agent/runlog"
	agentruntime "xiaoli/server/internal/agent/runtime"
	agentevent "xiaoli/server/internal/event"
)

type App struct {
	Config      localconfig.Config
	Runtime     agentruntime.Config
	Bus         agentevent.Bus
	Agent       *agentruntime.Agent
	RunLogDir   string
	unsubscribe agentevent.UnsubscribeFunc
}

type Options struct {
	ConfigPath string
	Prompt     string
	Ensure     bool
}

func New(opts Options) (*App, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, err
	}
	prompt, err := cfg.LoadPrompt(opts.Prompt)
	if err != nil {
		return nil, err
	}
	rt, err := cfg.RuntimeConfig(prompt)
	if err != nil {
		return nil, err
	}
	bus := agentevent.NewBus()
	runDir := cfg.RunLogDir()
	unsubscribe := runlog.Subscribe(bus, runDir)
	agent := agentruntime.NewAgent(rt, bus)
	if agent == nil {
		unsubscribe()
		return nil, fmt.Errorf("agent initialization failed; check model configuration and API key")
	}
	return &App{
		Config:      cfg,
		Runtime:     rt,
		Bus:         bus,
		Agent:       agent,
		RunLogDir:   runDir,
		unsubscribe: unsubscribe,
	}, nil
}

func (a *App) Close() {
	if a != nil && a.unsubscribe != nil {
		a.unsubscribe()
		a.unsubscribe = nil
	}
}

func loadConfig(opts Options) (localconfig.Config, error) {
	if opts.Ensure {
		return localconfig.EnsureDefaults(opts.ConfigPath)
	}
	return localconfig.Load(opts.ConfigPath)
}
