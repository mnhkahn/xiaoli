package admin

import (
	"testing"
	"time"
)

func TestWorkflowDefinitionsUseConfiguredChatReactAgent(t *testing.T) {
	cfg := testConfig()
	cfg.ChatReact = parseChatReact(settingsWorkflowAgent{MaxSteps: 16, Timeout: "180s"})
	srv := NewServer(cfg)

	def := srv.workflowByID("chat_react")
	if def == nil {
		t.Fatal("chat_react workflow not found")
	}
	if def.Agent.MaxSteps != 16 {
		t.Fatalf("chat_react max_steps = %d, want 16", def.Agent.MaxSteps)
	}
	if got := def.Agent.Timeout.String(); got != "3m0s" {
		t.Fatalf("chat_react timeout = %s, want 3m0s", got)
	}
}

func TestLarkMessageTimeoutFollowsChatReactWithMargin(t *testing.T) {
	cfg := testConfig()
	cfg.ChatReact = parseChatReact(settingsWorkflowAgent{MaxSteps: 8, Timeout: "120s"})
	srv := NewServer(cfg)

	if got := srv.larkMessageTimeout(); got != 150*time.Second {
		t.Fatalf("lark message timeout = %s, want 150s", got)
	}
}

func TestWorkflowDefinitionsIncludeChatAndCronJobs(t *testing.T) {
	cfg := testConfig()
	hour := 8
	minute := 0
	cfg.Workflows = parseWorkflows(map[string]settingsWorkflowDef{
		"study_monitor": {
			Name: "学习状态监控", Enabled: true,
			Trigger: settingsWorkflowTrigger{Every: "10m", Timezone: "Asia/Shanghai", StartHour: 17, EndHour: 21},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 6, Timeout: "150s"},
		},
		"morning_greeting": {
			Name: "早安问候", Enabled: true,
			Trigger: settingsWorkflowTrigger{Timezone: "Asia/Shanghai", AtHour: &hour, AtMinute: &minute},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 4, Timeout: "120s"},
		},
	})
	srv := NewServer(cfg)

	defs := srv.workflowDefinitions()
	seen := map[string]bool{}
	for _, def := range defs {
		seen[def.ID] = true
		if def.Agent.Name != "dispatch_agent" || def.Agent.Mode != "react" || def.Agent.MaxSteps <= 0 {
			t.Fatalf("workflow %s agent = %#v, want dispatch react agent", def.ID, def.Agent)
		}
	}
	for _, want := range []string{"chat_react", "study_monitor", "morning_greeting"} {
		if !seen[want] {
			t.Fatalf("workflow %q missing from %#v", want, defs)
		}
	}
}

func TestSchedulesUseCronWorkflowDefinitions(t *testing.T) {
	cfg := testConfig()
	hour := 8
	minute := 0
	cfg.Workflows = parseWorkflows(map[string]settingsWorkflowDef{
		"study_monitor": {
			Name: "学习状态监控", Enabled: true,
			Trigger: settingsWorkflowTrigger{Every: "10m", Timezone: "Asia/Shanghai", StartHour: 17, EndHour: 21},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 6, Timeout: "150s"},
		},
		"morning_greeting": {
			Name: "早安问候", Enabled: true,
			Trigger: settingsWorkflowTrigger{Timezone: "Asia/Shanghai", AtHour: &hour, AtMinute: &minute},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 4, Timeout: "120s"},
		},
	})
	srv := NewServer(cfg)

	schedules := srv.schedules()
	if len(schedules) != 2 {
		t.Fatalf("schedules length = %d, want 2", len(schedules))
	}
	for _, item := range schedules {
		if item["agent"] != "dispatch_agent" || item["mode"] != "react" {
			t.Fatalf("schedule %#v should expose workflow agent metadata", item)
		}
	}
}
