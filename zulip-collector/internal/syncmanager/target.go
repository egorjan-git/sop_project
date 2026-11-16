package syncmanager

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrTargetNotFound = errors.New("sync target not found")

// постоянное задание на сбор сообщений
type Target struct {
	ID               string
	ChannelID        int64
	PeriodStart      time.Time
	PeriodEnd        time.Time
	SelectionVersion int
	Config           json.RawMessage

	HighWatermarkID  *int64
	LastSuccessfulAt *time.Time
	Enabled          bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// интерфейс для работы с заданиями
type TargetRepository interface {
	CreateTarget(
		ctx context.Context,
		target Target,
	) (Target, error)

	GetTarget(
		ctx context.Context,
		targetID string,
	) (Target, error)
}
