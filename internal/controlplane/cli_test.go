package controlplane

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/credentials"
)

func TestFailedEnrollmentCommandDoesNotPublishAnEmptyPackage(t *testing.T) {
	db := testDB(t)
	directory := t.TempDir()
	config, authority, output := filepath.Join(directory, "control.json"), filepath.Join(directory, "private", "authority.json"), filepath.Join(directory, "enroll.json")
	if err := Command(t.Context(), db, []string{
		"init", "--config", config, "--authority", authority,
		"--control-address", "localhost:7443", "--enrollment-url", "https://localhost:7444/enroll",
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"enroll", "--config", config, "--name", "retryable-node", "--out", output}
	invalid := append([]string(nil), arguments...)
	invalid[4] = "   "
	if err := Command(t.Context(), db, invalid, io.Discard); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blocked enrollment=%v", err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed enrollment published an output file: %v", err)
	}
	if err := Command(t.Context(), db, arguments, io.Discard); err != nil {
		t.Fatal(err)
	}
	pack, err := credentials.ReadEnrollmentPackage(output)
	if err != nil || pack.Configuration.Identity.NodeID == "" {
		t.Fatalf("complete enrollment package unavailable: %v", err)
	}
	if err := Command(t.Context(), db, arguments, io.Discard); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing private package was not protected: %v", err)
	}
	var count int64
	if err := db.Model(&model.FleetNode{}).Where("name = ?", "retryable-node").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retry created %d nodes: %v", count, err)
	}
}
