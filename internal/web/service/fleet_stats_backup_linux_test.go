//go:build linux

package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFleetStatsDumpStopsWithLifecycle(t *testing.T) {
	fakePgDump(t, "printf 'PGDMP'; exec sleep 2")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	dump, err := (&ServerService{}).GetDbContext(ctx)
	if dump != nil {
		defer dump.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) || dump != nil || time.Since(started) >= time.Second {
		t.Fatalf("fleet dump outlived host lifecycle: dump=%v error=%v elapsed=%v", dump != nil, err, time.Since(started))
	}
}
