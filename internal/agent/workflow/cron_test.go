package workflow

import (
	"context"
	"testing"
	"time"
)

func TestCronSlotUsesIntervalWindow(t *testing.T) {
	spec := CronSpec{Every: 10 * time.Minute, Timezone: "Asia/Shanghai", StartHour: 17, EndHour: 21}
	inWindow := time.Date(2026, 6, 18, 17, 12, 0, 0, time.FixedZone("CST", 8*3600))
	outWindow := time.Date(2026, 6, 18, 9, 12, 0, 0, time.FixedZone("CST", 8*3600))

	if slot := CronSlot(spec, inWindow); slot == nil {
		t.Fatal("CronSlot() = nil, want slot inside window")
	}
	if slot := CronSlot(spec, outWindow); slot != nil {
		t.Fatalf("CronSlot() = %d, want nil outside window", *slot)
	}
}

func TestCronSlotUsesDailyTime(t *testing.T) {
	hour := 8
	minute := 30
	spec := CronSpec{Timezone: "Asia/Shanghai", AtHour: &hour, AtMinute: &minute}
	atTime := time.Date(2026, 6, 18, 8, 30, 20, 0, time.FixedZone("CST", 8*3600))
	other := time.Date(2026, 6, 18, 8, 31, 0, 0, time.FixedZone("CST", 8*3600))

	if slot := CronSlot(spec, atTime); slot == nil {
		t.Fatal("CronSlot() = nil, want daily slot")
	}
	if slot := CronSlot(spec, other); slot != nil {
		t.Fatalf("CronSlot() = %d, want nil outside minute", *slot)
	}
}

func TestCronSchedulerRunDueDeduplicatesSlots(t *testing.T) {
	called := make(chan time.Time, 2)
	job := CronJob{
		Definition: Definition{
			ID:      "study_monitor",
			Enabled: true,
			Trigger: Trigger{Kind: TriggerCron, Cron: &CronSpec{Every: 10 * time.Minute, StartHour: 17, EndHour: 21}},
		},
		Run: func(_ context.Context, scheduledAt time.Time) error {
			called <- scheduledAt
			return nil
		},
	}
	scheduler := NewCronScheduler(CronSchedulerConfig{Jobs: []CronJob{job}})
	now := time.Date(2026, 6, 18, 17, 12, 0, 0, time.UTC)

	scheduler.RunDue(context.Background(), now)
	scheduler.RunDue(context.Background(), now.Add(time.Minute))

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("job was not called")
	}
	select {
	case <-called:
		t.Fatal("job was called twice for same slot")
	case <-time.After(20 * time.Millisecond):
	}
}
