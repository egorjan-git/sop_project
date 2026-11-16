package syncmanager

type Mode string

const (
	ModeInitial     Mode = "initial"
	ModeIncremental Mode = "incremental"
	ModeReconcile   Mode = "reconcile"
)
