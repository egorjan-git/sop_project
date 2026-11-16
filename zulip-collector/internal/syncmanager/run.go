package syncmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (m Mode) Valid() bool {
	switch m {
	case ModeInitial, ModeIncremental, ModeReconcile:
		return true
	default:
		return false
	}
}

type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

var (
	ErrRunNotFound          = errors.New("sync run not found")
	ErrRunNotRunning        = errors.New("sync run is not running")
	ErrRunAlreadyActive     = errors.New("target already has a running sync")
	ErrTargetDisabled       = errors.New("sync target is disabled")
	ErrChannelNotAllowed    = errors.New("channel is not approved")
	ErrCheckpointIncomplete = errors.New("sync checkpoint is incomplete")
)

// статистика одного запуска
type RunStats struct {
	ProcessedPages   int
	FetchedMessages  int
	SelectedMessages int
	InsertedMessages int
	UpdatedMessages  int
}

func (s RunStats) Validate() error {
	if s.ProcessedPages < 0 ||
		s.FetchedMessages < 0 ||
		s.SelectedMessages < 0 ||
		s.InsertedMessages < 0 ||
		s.UpdatedMessages < 0 {
		return fmt.Errorf("sync counters cannot be negative")
	}

	return nil
}

// состояние конкретного запуска
type Run struct {
	ID       int64
	TargetID string
	ScopeID  string

	Mode   Mode
	Status RunStatus

	PeriodStart time.Time
	PeriodEnd   time.Time
	ScopeConfig json.RawMessage

	Stats          RunStats
	HistoryLimited *bool
	ErrorSummary   *string

	StartedAt  time.Time
	FinishedAt *time.Time
}

// операции с историей запусков
type RunRepository interface {
	StartRun(
		ctx context.Context,
		targetID string,
		mode Mode,
	) (Run, error)

	GetRun(
		ctx context.Context,
		runID int64,
	) (Run, error)

	CompleteRun(
		ctx context.Context,
		runID int64,
	) error

	FailRun(
		ctx context.Context,
		runID int64,
		summary string,
	) error
}
