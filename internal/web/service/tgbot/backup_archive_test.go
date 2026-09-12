package tgbot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/mymmrac/telego"
)

func TestStoredBackupOnlyReachesConfiguredAdministratorChats(t *testing.T) {
	var chats []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			http.Error(w, "invalid multipart", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		chats = append(chats, r.FormValue("chat_id"))
		file, header, err := r.FormFile("document")
		if err != nil {
			t.Error(err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil || string(body) != "encrypted archive bytes" || header.Filename != "snapshot.dump.pui" {
			t.Errorf("upload = %q, %s, %v", body, header.Filename, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":11,"type":"private"}}}`)
	}))
	defer server.Close()
	sender, err := telego.NewBot("1234567890:aaaabbbbaaaabbbbaaaabbbbaaaabbbbccc", telego.WithAPIServer(server.URL), telego.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	tgBotMutex.Lock()
	previousBot, previousRunning, previousAdmins := bot, isRunning, adminIds
	bot, isRunning, adminIds = sender, true, []int64{11, 22}
	tgBotMutex.Unlock()
	t.Cleanup(func() {
		tgBotMutex.Lock()
		bot, isRunning, adminIds = previousBot, previousRunning, previousAdmins
		tgBotMutex.Unlock()
	})
	file, err := os.CreateTemp(t.TempDir(), "archive-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("encrypted archive bytes"); err != nil {
		t.Fatal(err)
	}
	if err := SendBackupArchive(context.Background(), file, "snapshot.dump.pui"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(chats, []string{"11", "22"}) {
		t.Fatalf("recipients = %v, want only configured admins", chats)
	}
}
