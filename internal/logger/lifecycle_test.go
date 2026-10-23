package logger

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/op/go-logging"
)

func TestLoggerReinitReusesOpenRotator(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUI_LOG_FOLDER", dir)
	t.Cleanup(CloseLogger)
	InitLogger(logging.CRITICAL)
	Debug("first write")
	first := fileRotate
	InitLogger(logging.CRITICAL)
	Debug("second write")
	if fileRotate != first {
		t.Fatal("log-level reinitialization abandoned the open file rotator")
	}
	path := filepath.Join(dir, logFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	CloseLogger()
	Debug("after close")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("logging after CloseLogger reopened the retired file: %v", err)
	}
}

func TestLoggerLifecycleConcurrent(t *testing.T) {
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	t.Cleanup(CloseLogger)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 12 {
			InitLogger(logging.CRITICAL)
		}
	})
	workers.Go(func() {
		for range 12 {
			Debug("concurrent lifecycle")
			Infof("concurrent lifecycle %d", 1)
		}
	})
	workers.Go(func() {
		for range 12 {
			CloseLogger()
		}
	})
	workers.Wait()
}
