package tgbot

import (
	"os"
	"strings"
	"testing"
)

/*
A backup that did not happen must not be reported as one that did.

sendBackupData opens by asserting a backup exists: the hostname and the backup
time are sent before anything is attempted. It then had exits that produced no
chat output at all -- the dump was never built (pg_dump missing, a bad DSN, a
non-zero exit), or the upload failed. The admin got a header naming a time and
then silence, which reads as "the file is on its way".

That is the worst shape a backup failure can take. An operator who believes the
scheduled backup is arriving does not go looking for it, and finds out when they
need to restore.

Asserted on the source, which is this package's idiom for the same reason
TestTheDraftStateBranchChecksTheSender uses it: sendBackupData writes through the
package-level `bot` global, and standing a live telego handler up would test the
framework rather than the guard. The property is "no exit from this function is
silent", and that is what the source says. F273.
*/
func TestNoBackupFailureExitIsSilent(t *testing.T) {
	body, err := os.ReadFile("tgbot_report.go")
	if err != nil {
		t.Fatalf("read tgbot_report.go: %v", err)
	}

	open := "func (t *Tgbot) sendBackupData(chatId int64, dump *service.DumpFile, dbFilename string) {"
	start := strings.Index(string(body), open)
	if start < 0 {
		t.Fatal("sendBackupData was renamed or removed; this guard is now vacuous")
	}
	fn := string(body)[start:]
	if end := strings.Index(fn, "\n}\n"); end >= 0 {
		fn = fn[:end]
	}

	// The dump-never-built exit.
	if !strings.Contains(fn, "if dump != nil && dump.Size > 0 {") {
		t.Error(`sendBackupData tests the dump for nil rather than size, so a zero-length dump is uploaded as though it were an archive; pg_restore cannot read it`)
	}
	if !strings.Contains(fn, "} else {") {
		t.Error("the branch that skips the upload has no else, so a backup that was never built is reported only by the header claiming one was taken")
	}

	// The upload-failed exit. This is the one an earlier version of the fix
	// omitted, and the likelier failure: ordinary messages retry with backoff
	// while this document send has none.
	uploadErr := strings.Index(fn, `logger.Error("Error in uploading backup: ", err)`)
	if uploadErr < 0 {
		t.Fatal("the upload error branch was restructured; this guard is now vacuous")
	}
	window := fn[uploadErr:]
	if end := strings.Index(window, "\t\t}"); end >= 0 {
		window = window[:end]
	}
	if !strings.Contains(window, "backupDbFailed") {
		t.Errorf("a failed backup UPLOAD is logged and never mentioned in the chat, on a chat that is demonstrably working:\n%s", window)
	}

	// Both exits must speak, and both through a key that exists in the locales.
	if n := strings.Count(fn, "backupDbFailed"); n != 2 {
		t.Errorf("want both failure exits to notify, found %d notification(s)", n)
	}
}
