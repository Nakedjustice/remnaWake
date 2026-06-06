package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"
)

type Job struct {
	task     func(context.Context)
	logger   *slog.Logger
	timezone string
	runAt    string
}

func New(task func(context.Context), logger *slog.Logger, timezone, runAt string) *Job {
	return &Job{
		task:     task,
		logger:   logger,
		timezone: timezone,
		runAt:    runAt,
	}
}

func (j *Job) Start(ctx context.Context) (gocron.Scheduler, error) {
	loc, err := time.LoadLocation(j.timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", j.timezone, err)
	}

	parsed, err := time.ParseInLocation("15:04", j.runAt, loc)
	if err != nil {
		return nil, fmt.Errorf("parse run_at %q: %w", j.runAt, err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}

	_, err = s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(
			gocron.NewAtTime(uint(parsed.Hour()), uint(parsed.Minute()), 0),
		)),
		gocron.NewTask(func() {
			taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					j.logger.Error("task panic", "err", fmt.Sprintf("%v", r))
				}
			}()
			j.task(taskCtx)
		}),
		gocron.WithName("notify-daily"),
	)
	if err != nil {
		return nil, fmt.Errorf("register job: %w", err)
	}

	s.Start()
	j.logger.Info("scheduler started",
		"timezone", j.timezone,
		"run_at", j.runAt,
		"next_run", nextRun(loc, parsed).Format(time.RFC3339),
	)
	return s, nil
}

func nextRun(loc *time.Location, t time.Time) time.Time {
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
