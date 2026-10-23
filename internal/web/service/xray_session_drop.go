package service

import "go.uber.org/atomic"

var (
	sessionDropRequested atomic.Uint64
	sessionDropCompleted atomic.Uint64
)

func queueXraySessionDrop() {
	sessionDropRequested.Inc()
}

func pendingXraySessionDrop() (uint64, bool) {
	revision := sessionDropRequested.Load()
	return revision, revision != sessionDropCompleted.Load()
}
