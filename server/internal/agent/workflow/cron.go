package workflow

import (
	"context"
	"fmt"
	"time"
)

type CronJob struct {
	Definition Definition
	Run        func(context.Context, time.Time) error
}

type CronScheduler struct {
	jobs     []CronJob
	tick     time.Duration
	now      func() time.Time
	lastSlot map[string]int64
}

type CronSchedulerConfig struct {
	Jobs []CronJob
	Tick time.Duration
	Now  func() time.Time
}

func NewCronScheduler(cfg CronSchedulerConfig) *CronScheduler {
	tick := cfg.Tick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &CronScheduler{jobs: cfg.Jobs, tick: tick, now: now, lastSlot: map[string]int64{}}
}

func (s *CronScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		s.RunDue(ctx, s.now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *CronScheduler) RunDue(ctx context.Context, now time.Time) {
	for _, job := range s.jobs {
		if !job.Definition.Enabled || job.Definition.Trigger.Kind != TriggerCron || job.Definition.Trigger.Cron == nil || job.Run == nil {
			continue
		}
		slot := CronSlot(*job.Definition.Trigger.Cron, now)
		if slot == nil {
			continue
		}
		key := job.Definition.ID
		if key == "" {
			key = fmt.Sprintf("%p", job.Run)
		}
		if previous, ok := s.lastSlot[key]; ok && previous == *slot {
			continue
		}
		s.lastSlot[key] = *slot
		go func(job CronJob, scheduledAt time.Time) {
			_ = job.Run(ctx, scheduledAt)
		}(job, now)
	}
}

func CronSlot(spec CronSpec, checkedAt time.Time) *int64 {
	location, err := time.LoadLocation(spec.Timezone)
	if err == nil {
		checkedAt = checkedAt.In(location)
	}
	if spec.AtHour != nil || spec.AtMinute != nil {
		hour := intValue(spec.AtHour, 0)
		minute := intValue(spec.AtMinute, 0)
		if checkedAt.Hour() != hour || checkedAt.Minute() != minute {
			return nil
		}
		dayStart := time.Date(checkedAt.Year(), checkedAt.Month(), checkedAt.Day(), 0, 0, 0, 0, checkedAt.Location())
		return int64Ptr(dayStart.Unix())
	}
	if !InWindow(spec, checkedAt) {
		return nil
	}
	interval := spec.Every
	if interval < time.Minute {
		interval = time.Minute
	}
	slot := checkedAt.Unix() - checkedAt.Unix()%int64(interval.Seconds())
	return &slot
}

func InWindow(spec CronSpec, checkedAt time.Time) bool {
	start := spec.StartHour
	end := spec.EndHour
	hour := checkedAt.Hour()
	if start == end {
		return true
	}
	if start < end {
		return start <= hour && hour < end
	}
	return hour >= start || hour < end
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func int64Ptr(value int64) *int64 {
	return &value
}
