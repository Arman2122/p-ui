package controller

import (
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/service/panel"
)

type BackupController struct{ service service.BackupService }

func NewBackupController(g *gin.RouterGroup) *BackupController {
	a := &BackupController{}
	g = g.Group("/backups", requireConfigAdmin)
	g.GET("", a.status)
	g.POST("/schedule", a.schedule)
	g.POST("/create", middleware.LongRequest(), a.create)
	g.GET("/:id/download", middleware.LongRequest(), a.download)
	g.POST("/:id/restore", middleware.LongRequest(), a.restore)
	g.POST("/upload", middleware.LongRequest(), a.upload)
	g.POST("/:id/telegram", middleware.LongRequest(), a.telegram)
	return a
}

func (a *BackupController) status(c *gin.Context) {
	view, err := a.service.Status()
	jsonObj(c, view, err)
}

func (a *BackupController) schedule(c *gin.Context) {
	var value service.BackupSchedule
	if err := c.ShouldBind(&value); err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, nil, (&service.SettingService{}).SetBackupSchedule(value))
}

func (a *BackupController) create(c *gin.Context) {
	var options service.BackupCreateOptions
	if err := c.ShouldBind(&options); err != nil {
		jsonObj(c, nil, err)
		return
	}
	defer clearBackupPassphrase(c)
	view, err := a.service.CreateWithOptions(c.Request.Context(), "manual", options)
	options.Passphrase = ""
	jsonObj(c, view, err)
}

func (a *BackupController) download(c *gin.Context) {
	file, err := a.service.Open(c.Param("id"))
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="`+filepath.Base(file.Name())+`"`)
	c.DataFromReader(http.StatusOK, info.Size(), "application/octet-stream", file, nil)
}

func backupRestoreConfirmed(c *gin.Context) bool {
	if c.PostForm("confirm") != "RESTORE" {
		jsonObj(c, nil, errors.New("confirm must be RESTORE"))
		return false
	}
	return true
}

func (a *BackupController) restore(c *gin.Context) {
	if !backupRestoreConfirmed(c) {
		return
	}
	defer clearBackupPassphrase(c)
	view, err := a.service.RestoreWithPassphrase(c.Param("id"), c.PostForm("passphrase"))
	a.restoreResult(c, view, err)
}

func (a *BackupController) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<30)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	if !backupRestoreConfirmed(c) {
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	file, err := header.Open()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	defer file.Close()
	defer clearBackupPassphrase(c)
	view, err := a.service.RestoreUploadWithPassphrase(file, header.Filename, c.PostForm("passphrase"))
	a.restoreResult(c, view, err)
}

func clearBackupPassphrase(c *gin.Context) {
	delete(c.Request.PostForm, "passphrase")
	delete(c.Request.Form, "passphrase")
	if c.Request.MultipartForm != nil {
		delete(c.Request.MultipartForm.Value, "passphrase")
	}
}

func (a *BackupController) restoreResult(c *gin.Context, view service.BackupRecordView, err error) {
	jsonObj(c, view, err)
	if err == nil && c.GetBool("api_authed") {
		_ = (&panel.PanelService{}).RestartPanel(2 * time.Second)
	}
}

func (a *BackupController) telegram(c *gin.Context) {
	view, err := a.service.SendToTelegram(c.Request.Context(), c.Param("id"))
	jsonObj(c, view, err)
}
