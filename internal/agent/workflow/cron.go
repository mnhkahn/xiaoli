package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type CronJob struct {
	Definition Definition
	Run        func(context.Context, time.Time) error
}

type CronScheduler struct {
	jobs     []CronJob
	provider func() []CronJob
	tick     time.Duration
	now      func() time.Time
	lastSlot map[string]int64
	inFlight map[string]bool
	mu       sync.Mutex
}

type CronSchedulerConfig struct {
	Jobs []CronJob
	// JobsProvider 非空时，每个 tick 重新获取任务列表，使运行时增删的提醒即时生效。
	JobsProvider func() []CronJob
	Tick         time.Duration
	Now          func() time.Time
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
	return &CronScheduler{
		jobs:     cfg.Jobs,
		provider: cfg.JobsProvider,
		tick:     tick,
		now:      now,
		lastSlot: map[string]int64{},
		inFlight: map[string]bool{},
	}
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

func (s *CronScheduler) currentJobs() []CronJob {
	if s.provider != nil {
		return s.provider()
	}
	return s.jobs
}

func (s *CronScheduler) RunDue(ctx context.Context, now time.Time) {
	for _, job := range s.currentJobs() {
		if !job.Definition.Enabled || job.Definition.Trigger.Kind != TriggerCron || job.Definition.Trigger.Cron == nil || job.Run == nil {
			continue
		}
		spec := *job.Definition.Trigger.Cron
		slot := CronSlot(spec, now)
		if slot == nil {
			continue
		}
		key := job.Definition.ID
		if key == "" {
			key = fmt.Sprintf("%p", job.Run)
		}

		isOnce := spec.At != nil

		s.mu.Lock()
		// 防止同一任务并发执行（上次还没跑完，本 tick 跳过）
		if s.inFlight[key] {
			s.mu.Unlock()
			continue
		}
		// 周期性任务（daily/interval）：同一 slot 已触发过则跳过。
		// 一次性任务不用 slot 去重——失败后需在后续 tick 重试，靠存储的 fired_at 防重复。
		if !isOnce {
			if previous, ok := s.lastSlot[key]; ok && previous == *slot {
				s.mu.Unlock()
				continue
			}
			s.lastSlot[key] = *slot
		}
		s.inFlight[key] = true
		s.mu.Unlock()

		go func(job CronJob, key string, scheduledAt time.Time) {
			defer func() {
				s.mu.Lock()
				delete(s.inFlight, key)
				s.mu.Unlock()
			}()
			_ = job.Run(ctx, scheduledAt)
		}(job, key, now)
	}
}

func CronSlot(spec CronSpec, checkedAt time.Time) *int64 {
	location, err := time.LoadLocation(spec.Timezone)
	if err == nil {
		checkedAt = checkedAt.In(location)
	}
	if spec.At != nil {
		if checkedAt.Before(*spec.At) {
			return nil
		}
		return int64Ptr(spec.At.Unix())
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
