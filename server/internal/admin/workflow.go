package admin

import (
	"fmt"
	"strings"
	"time"

	agentworkflow "xiaoli/server/internal/agent/workflow"
)

func (s *AdminServer) workflowDefinitions() []agentworkflow.Definition {
	defs := []agentworkflow.Definition{DefinitionChatReact()}

	studyMeta := map[string]any{
		"window":           fmt.Sprintf("%02d:00-%02d:00", s.cfg.StudyMonitorStartHour, s.cfg.StudyMonitorEndHour),
		"interval_seconds": int(defaultDuration(s.cfg.StudyMonitorInterval, 10*time.Minute).Seconds()),
		"camera_tool":      s.cfg.StudyMonitorCameraTool,
		"reminder_text":    s.cfg.StudyMonitorReminder,
		"device_ids":       s.cfg.StudyMonitorDeviceIDs,
	}
	defs = append(defs, agentworkflow.Definition{
		ID:          "study_monitor",
		Name:        "学习状态监控",
		Description: "在设定时间窗内定时调用摄像头检查学习状态，并按需发送语音提醒和飞书通知。",
		Enabled:     s.cfg.StudyMonitorEnabled,
		Trigger: agentworkflow.Trigger{
			Kind: agentworkflow.TriggerCron,
			Cron: &agentworkflow.CronSpec{
				Every:     defaultDuration(s.cfg.StudyMonitorInterval, 10*time.Minute),
				Timezone:  s.cfg.StudyMonitorTimezone,
				StartHour: s.cfg.StudyMonitorStartHour,
				EndHour:   s.cfg.StudyMonitorEndHour,
			},
		},
		Agent:    agentworkflow.AgentSpec{Name: "dispatch_agent", Mode: "react", MaxSteps: 6, Timeout: s.cfg.StudyMonitorToolTimeout + 30*time.Second},
		Metadata: studyMeta,
	})

	hour := clampInt(s.cfg.MorningGreetingHour, 0, 23, 8)
	minute := clampInt(s.cfg.MorningGreetingMinute, 0, 59, 0)
	greetingMeta := map[string]any{
		"time":       fmt.Sprintf("%02d:%02d", hour, minute),
		"text":       firstText(strings.TrimSpace(s.cfg.MorningGreetingText), "早上好。"),
		"device_ids": s.cfg.MorningGreetingDeviceIDs,
	}
	defs = append(defs, agentworkflow.Definition{
		ID:          "morning_greeting",
		Name:        "早安问候",
		Description: "每天早上固定时间向在线设备播放问候语；没有在线设备时跳过，不补播。",
		Enabled:     s.cfg.MorningGreetingEnabled,
		Trigger: agentworkflow.Trigger{
			Kind: agentworkflow.TriggerCron,
			Cron: &agentworkflow.CronSpec{
				Timezone: s.cfg.MorningGreetingTimezone,
				AtHour:   &hour,
				AtMinute: &minute,
			},
		},
		Agent:    agentworkflow.AgentSpec{Name: "dispatch_agent", Mode: "react", MaxSteps: 4, Timeout: 120 * time.Second},
		Metadata: greetingMeta,
	})
	return defs
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
