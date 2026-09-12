package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/global"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestBackupUploadAboveGlobalLimitReachesArchiveValidation(t *testing.T) {
	initTestDB(t)
	privateBin := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", privateBin)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	t.Setenv("PUI_DEBUG", "false")
	settings := &service.SettingService{}
	if err := settings.SetBasePath("/backup-limit/"); err != nil {
		t.Fatal(err)
	}
	const marker = "backup-upload-no-restore"
	if err := database.GetDB().Create(&model.Setting{Key: marker, Value: "untouched"}).Error; err != nil {
		t.Fatal(err)
	}
	const token = "administrator-token-for-backup-upload-limit-test"
	if err := database.GetDB().Create(&model.ApiToken{Name: "backup-upload-limit", Token: crypto.HashTokenSHA256(token), Enabled: true, Scope: model.ApiTokenScopeAdmin}).Error; err != nil {
		t.Fatal(err)
	}
	previous := global.GetWebServer()
	server := NewServer()
	server.cron = cron.New(cron.WithSeconds())
	global.SetWebServer(server)
	t.Cleanup(func() { server.cancel(); global.SetWebServer(previous) })
	engine, err := server.initRouter()
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("confirm", "RESTORE"); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "invalid-large.dump.pui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Repeat([]byte("x"), 11<<20)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/backup-limit/panel/api/backups/upload", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	if request.ContentLength <= 10<<20 {
		t.Fatal("fixture does not exceed the global 10 MiB body limit")
	}
	before := database.GetDB()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	var response struct {
		Success bool                     `json:"success"`
		Msg     string                   `json:"msg"`
		Obj     service.BackupRecordView `json:"obj"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("status %d: decode response: %v", recorder.Code, err)
	}
	if recorder.Code != http.StatusOK || response.Success || response.Obj.Operation != "restore" || response.Obj.Source != "upload" || response.Obj.Status != "failed" || response.Obj.Error != service.ErrBackupAuthentication.Error() {
		t.Fatalf("upload did not reach archive authentication: HTTP %d, %+v", recorder.Code, response)
	}
	if database.GetDB() != before {
		t.Fatal("invalid upload reopened the database")
	}
	var sentinel model.Setting
	if err := before.Where("key = ?", marker).First(&sentinel).Error; err != nil || sentinel.Value != "untouched" {
		t.Fatalf("invalid upload changed the database: %+v, %v", sentinel, err)
	}
	manifest := filepath.Join(privateBin, "backups", response.Obj.ID+".json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("validation failure was not recorded in the private backup directory: %v", err)
	}
	archives, err := filepath.Glob(filepath.Join(privateBin, "backups", "*.dump*"))
	if err != nil || len(archives) != 0 {
		t.Fatalf("invalid upload created an archive: %v, %v", archives, err)
	}
}
