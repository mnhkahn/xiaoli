package admin

import "testing"

func TestWorkflowDefinitionsIncludeChatAndCronJobs(t *testing.T) {
	cfg := testConfig()
	cfg.StudyMonitorEnabled = true
	cfg.MorningGreetingEnabled = true
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
	cfg.StudyMonitorEnabled = true
	cfg.MorningGreetingEnabled = true
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
