package tgbot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	json_util "github.com/Arman2122/p-ui/internal/util/json_util"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
The Telegram backup shipped the config.json on disk, which is not the config
that is running.

Process.Start writes that file once, at start. Every hot apply after it goes
through the gRPC API and updates only the process's in-memory snapshot, so an
operator who added ten clients since the last restart got a backup describing
none of them — while the pg_dump alongside it, the actual restore authority,
described all ten. A diagnostic artefact that disagrees with the thing it sits
next to is worse than no artefact.

Fixed at the consumer. The producer was left alone deliberately: writing the
file inside SetConfig would marshal and fsync under p.mu on every client add,
blocking GetConfig, IsRunning and GetAPIPort — which sit on the traffic-poll
path this repo scale-tests at 200k clients.

So `cat config.json` on the box stays stale, and that is a separate decision
about where a snapshot is persisted. It is bounded: ComputeHotDiff refuses the
hot path unless log/dns/policy/api/stats are byte-identical, so getLogPath, the
only functional reader of the file, can never be misled. F012.
*/
func TestTheConfigBackupIsTheRunningConfigNotTheStaleFile(t *testing.T) {
	dir := t.TempDir()
	diskPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(diskPath, []byte(`{"inbounds":[{"tag":"stale-from-last-restart","port":1}]}`), 0o600); err != nil {
		t.Fatalf("seed disk config: %v", err)
	}

	running := &xray.Config{
		InboundConfigs: []xray.InboundConfig{{Tag: "added-since-restart", Port: 2}},
		Policy:         json_util.RawMessage(`{}`),
	}

	data, err := configBackup(running, diskPath)
	if err != nil {
		t.Fatalf("configBackup: %v", err)
	}

	var got xray.Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the backup is not parseable as a config: %v\n%s", err, data)
	}
	if len(got.InboundConfigs) != 1 {
		t.Fatalf("want one inbound, got %d: %s", len(got.InboundConfigs), data)
	}
	if got.InboundConfigs[0].Tag != "added-since-restart" {
		t.Fatalf("the backup describes the file on disk, not the core that is running: tag = %q", got.InboundConfigs[0].Tag)
	}
}

// With no running core there is nothing in memory to prefer, so the file is
// still the best answer available — and must not become an error.
func TestTheConfigBackupFallsBackToDiskWhenNothingIsRunning(t *testing.T) {
	dir := t.TempDir()
	diskPath := filepath.Join(dir, "config.json")
	want := []byte(`{"inbounds":[{"tag":"last-known","port":1}]}`)
	if err := os.WriteFile(diskPath, want, 0o600); err != nil {
		t.Fatalf("seed disk config: %v", err)
	}

	data, err := configBackup(nil, diskPath)
	if err != nil {
		t.Fatalf("configBackup with no running core: %v", err)
	}
	if string(data) != string(want) {
		t.Fatalf("want the file verbatim, got:\n%s", data)
	}
}

// And the shipped bytes match what a restart would write, so a backup taken
// while running and one taken after a restart are the same document.
func TestTheConfigBackupMatchesWhatStartWritesToDisk(t *testing.T) {
	running := &xray.Config{
		InboundConfigs: []xray.InboundConfig{{Tag: "t", Port: 443}},
		Policy:         json_util.RawMessage(`{"levels":{}}`),
	}

	data, err := configBackup(running, filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("configBackup: %v", err)
	}
	written, err := json.MarshalIndent(running, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != string(written) {
		t.Fatalf("the backup differs from what Process.Start writes:\ngot:\n%s\nwant:\n%s", data, written)
	}
}
