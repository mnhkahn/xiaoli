package admin

import (
	agentworkflow "github.com/mnhkahn/xiaoli-esp32/server/internal/agent/workflow"
	"testing"
	"time"
)

func TestPickOnlineDevice(t *testing.T) {
	devices := []Device{
		{DeviceID: "device-1"},
		{DeviceID: "device-2"},
		{DeviceID: "device-3"},
	}

	cases := []struct {
		name      string
		devices   []Device
		allowlist []string
		want      string
	}{
		{"empty allowlist returns first device", devices, nil, "device-1"},
		{"empty allowlist with empty device list", nil, nil, ""},
		{"allowlist matches first device", devices, []string{"device-1"}, "device-1"},
		{"allowlist matches middle device", devices, []string{"device-2"}, "device-2"},
		{"allowlist matches last device", devices, []string{"device-3"}, "device-3"},
		{"allowlist with no match returns empty", devices, []string{"device-99"}, ""},
		{"allowlist with multiple entries picks first online device that matches", devices, []string{"device-99", "device-2", "device-1"}, "device-1"},
		{"Mac hardwareUUID excluded when ESP32 MAC only", []Device{
			{DeviceID: "D3AEA19E-5592-54A4-A993-B4D8135AEA29"},
			{DeviceID: "28:84:85:8c:ef:f4"},
		}, []string{"28:84:85:8c:ef:f4"}, "28:84:85:8c:ef:f4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickOnlineDevice(tc.devices, tc.allowlist)
			if got != tc.want {
				t.Fatalf("pickOnlineDevice(%v, %v) = %q, want %q", tc.devices, tc.allowlist, got, tc.want)
			}
		})
	}
}

func TestParseStudyDecisionFromJSON(t *testing.T) {
	srv := NewServer(testConfig())
	value := map[string]any{
		"need_reminder": true,
		"posture":       "低头过近",
		"focus":         "正在写作业",
		"summary":       "需要调整坐姿",
		"reminder_text": "抬头一点。",
	}

	decision := srv.parseStudyDecision(value, "请坐直")

	if !decision.NeedReminder {
		t.Fatal("NeedReminder = false, want true")
	}
	if decision.ReminderText != "抬头一点。" {
		t.Fatalf("ReminderText = %q", decision.ReminderText)
	}
	if decision.AnalysisText == "" {
		t.Fatal("AnalysisText is empty")
	}
}

func TestParseStudyDecisionFromNestedJSONText(t *testing.T) {
	srv := NewServer(testConfig())
	value := map[string]any{
		"result": `{"need_reminder":false,"summary":"坐姿端正，认真学习"}`,
	}

	decision := srv.parseStudyDecision(value, "请坐直")

	if decision.NeedReminder {
		t.Fatal("NeedReminder = true, want false")
	}
	if decision.AnalysisText != "坐姿端正，认真学习" {
		t.Fatalf("AnalysisText = %q", decision.AnalysisText)
	}
}

func TestStudyMonitorSlotUsesConfiguredWindow(t *testing.T) {
	cfg := testConfig()
	cfg.Workflows = parseWorkflows(map[string]settingsWorkflowDef{
		"study_monitor": {
			Name: "学习状态监控", Enabled: true,
			Trigger: settingsWorkflowTrigger{Every: "10m", Timezone: "Asia/Shanghai", StartHour: 17, EndHour: 21},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 6, Timeout: "150s"},
		},
	})
	srv := NewServer(cfg)
	def := srv.workflowByID("study_monitor")
	if def == nil || def.Trigger.Cron == nil {
		t.Fatal("study_monitor workflow not found or has no cron spec")
	}
	cronSpec := def.Trigger.Cron
	inWindow := time.Date(2026, 5, 24, 17, 3, 20, 0, time.FixedZone("CST", 8*3600))
	outWindow := time.Date(2026, 5, 24, 21, 0, 0, 0, time.FixedZone("CST", 8*3600))

	if slot := agentworkflow.CronSlot(*cronSpec, inWindow); slot == nil {
		t.Fatal("slot is nil inside monitor window")
	}
	if slot := agentworkflow.CronSlot(*cronSpec, outWindow); slot != nil {
		t.Fatalf("slot = %v outside monitor window", *slot)
	}
}

func TestMorningGreetingSlotFiresAtConfiguredTime(t *testing.T) {
	cfg := testConfig()
	hour := 8
	minute := 0
	cfg.Workflows = parseWorkflows(map[string]settingsWorkflowDef{
		"morning_greeting": {
			Name: "早安问候", Enabled: true,
			Trigger: settingsWorkflowTrigger{Timezone: "Asia/Shanghai", AtHour: &hour, AtMinute: &minute},
			Agent:   settingsWorkflowAgent{Name: "dispatch_agent", Mode: "react", MaxSteps: 4, Timeout: "120s"},
		},
	})
	srv := NewServer(cfg)
	def := srv.workflowByID("morning_greeting")
	if def == nil || def.Trigger.Cron == nil {
		t.Fatal("morning_greeting workflow not found or has no cron spec")
	}
	cronSpec := def.Trigger.Cron
	before := time.Date(2026, 6, 4, 7, 59, 30, 0, time.FixedZone("CST", 8*3600))
	atTime := time.Date(2026, 6, 4, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	later := time.Date(2026, 6, 4, 8, 0, 59, 0, time.FixedZone("CST", 8*3600))

	if slot := agentworkflow.CronSlot(*cronSpec, before); slot != nil {
		t.Fatalf("slot = %v before greeting time", *slot)
	}
	firstSlot := agentworkflow.CronSlot(*cronSpec, atTime)
	if firstSlot == nil {
		t.Fatal("slot is nil at greeting time")
	}
	secondSlot := agentworkflow.CronSlot(*cronSpec, later)
	if secondSlot == nil || *secondSlot != *firstSlot {
		t.Fatalf("slot within same minute = %v, want %v", secondSlot, firstSlot)
	}
}

func TestBuildLarkPostPayloadIncludesImageAndReminder(t *testing.T) {
	srv := NewServer(testConfig())
	payload := srv.buildLarkPostPayload(studyLarkPayloadInput{
		DeviceID:       "device-1",
		AnalysisText:   "低头过近",
		NeedReminder:   true,
		ReminderText:   "请坐直",
		ImageKey:       "img-key",
		CheckedAt:      time.Date(2026, 5, 24, 18, 30, 0, 0, time.FixedZone("CST", 8*3600)),
		ReminderResult: "queued",
		ElapsedMS:      123,
	})

	content := payload["content"].(map[string]any)
	post := content["post"].(map[string]any)
	zh := post["zh_cn"].(map[string]any)
	if zh["title"] != "学习状态巡检 2026-05-24 18:30" {
		t.Fatalf("title = %q", zh["title"])
	}
	lines := zh["content"].([][]map[string]string)
	foundImage := false
	for _, line := range lines {
		for _, item := range line {
			if item["tag"] == "img" && item["image_key"] == "img-key" {
				foundImage = true
			}
		}
	}
	if !foundImage {
		t.Fatal("lark post payload did not include image")
	}
}
