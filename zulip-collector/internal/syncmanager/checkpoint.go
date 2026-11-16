package syncmanager

import (
	"context"
	"errors"
	"time"
)

var ErrCheckpointNotFound = errors.New("sync checkpoint not found")

// состояние обработки конкретного запуска
type Checkpoint struct {
	ScopeID  string
	TargetID string
	RunID    int64

	Cursor string
	Done   bool

	UpdatedAt time.Time
}

// операции чтения состояния запуска
type CheckpointRepository interface {
	GetRunCheckpoint(
		ctx context.Context,
		runID int64,
	) (Checkpoint, error)
}
