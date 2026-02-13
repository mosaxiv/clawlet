package cron

import (
	"path/filepath"
	"testing"
	"time"
)

func TestServiceAdd_RejectsInvalidSchedule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sched Schedule
	}{
		{
			name:  "every must be positive",
			sched: Schedule{Kind: "every", EveryMS: 0},
		},
		{
			name:  "cron expression must be valid",
			sched: Schedule{Kind: "cron", Expr: "not a cron"},
		},
		{
			name:  "at must be future",
			sched: Schedule{Kind: "at", AtMS: time.Now().Add(-1 * time.Minute).UnixMilli()},
		},
		{
			name:  "kind must be known",
			sched: Schedule{Kind: "unknown"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "cron.json")
			svc := NewService(path, nil)
			if _, err := svc.Add("test", tt.sched, Payload{Kind: "agent_turn", Message: "hello"}); err == nil {
				t.Fatalf("expected error for schedule %+v", tt.sched)
			}
		})
	}
}

func TestServiceAdd_AcceptsValidSchedule(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cron.json")
	svc := NewService(path, nil)

	job, err := svc.Add("test", Schedule{Kind: "every", EveryMS: 60_000}, Payload{Kind: "agent_turn", Message: "hello"})
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if job.State.NextRunAtMS <= time.Now().UnixMilli() {
		t.Fatalf("expected next run in the future, got %d", job.State.NextRunAtMS)
	}
}
