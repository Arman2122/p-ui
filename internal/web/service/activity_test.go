package service

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestActivityPersistsWithScopedPerUserReadState(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	second := model.User{Username: "second-reader", Role: "admin"}
	reseller := model.User{Username: "private-reader", Role: "reseller"}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	svc := ActivityService{}
	ctx := context.Background()
	first, err := svc.Record(ctx, model.ActivityEvent{Kind: "core.crash", Level: "error", TitleKey: "events.coreCrash.title"}, map[string]string{"core": "xray", "error": "password=should-never-be-stored"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := svc.Record(ctx, model.ActivityEvent{AudienceUserID: reseller.Id, Kind: "audit.clients.create", Level: "info"}, map[string]string{"actor": reseller.Username})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.Params["error"], "should-never-be-stored") {
		t.Fatal("raw engine credential was persisted")
	}
	firstID, _ := strconv.ParseInt(first.ID, 10, 64)
	if err := svc.MarkRead(ctx, admin.Id, true, firstID, 0); err != nil {
		t.Fatal(err)
	}
	page, err := (ActivityService{}).List(ctx, admin.Id, true, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != private.ID || page.Unread != 1 || page.NextBefore == 0 {
		t.Fatalf("unexpected newest page: %+v", page)
	}
	older, err := svc.List(ctx, admin.Id, true, page.NextBefore, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Items) != 1 || older.Items[0].ID != first.ID || !older.Items[0].Read {
		t.Fatalf("saved read state lost: %+v", older)
	}
	other, err := svc.List(ctx, second.Id, true, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if other.Unread != 2 {
		t.Fatalf("one user's read changed another's: %d", other.Unread)
	}
	own, err := svc.List(ctx, reseller.Id, false, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(own.Items) != 1 || own.Items[0].ID != private.ID {
		t.Fatalf("reseller saw another audience: %+v", own)
	}
	if err := svc.MarkRead(ctx, reseller.Id, false, firstID, 0); err == nil || err.Error() != "activity event is not available" {
		t.Fatalf("cross-audience acknowledgement = %v", err)
	}
	if _, err := svc.List(ctx, 0, false, 0, 100); err == nil {
		t.Fatal("unauthenticated activity access succeeded")
	}
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDB(); err != nil {
		t.Fatal(err)
	}
	reopened, err := svc.List(ctx, admin.Id, true, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Generation == page.Generation || reopened.Unread != 1 {
		t.Fatalf("reopened state: %+v", reopened)
	}
}

func TestActivityRetentionAndReadAllHighWaterMark(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Exec(`INSERT INTO activity_events (audience_user_id,actor_user_id,kind,level,title_key,message_key,params,created_at)
		SELECT 0,0,'test','info','','','{}',? FROM generate_series(1,?)`, now.UnixMilli(), ActivityMaxRows+4).Error; err != nil {
		t.Fatal(err)
	}
	old := model.ActivityEvent{Kind: "expired", Level: "info", Params: "{}", CreatedAt: now.Add(-ActivityRetention - time.Hour).UnixMilli()}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ActivityRead{UserID: admin.Id, EventID: old.Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := (ActivityService{}).Prune(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var events, reads int64
	db.Model(&model.ActivityEvent{}).Count(&events)
	db.Model(&model.ActivityRead{}).Count(&reads)
	if events != ActivityMaxRows || reads != 0 {
		t.Fatalf("retention: events=%d reads=%d", events, reads)
	}
	svc := ActivityService{}
	page, err := svc.List(context.Background(), admin.Id, true, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	through, _ := strconv.ParseInt(page.Items[0].ID, 10, 64)
	newer, err := svc.Record(context.Background(), model.ActivityEvent{Kind: "new", Level: "info"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRead(context.Background(), admin.Id, true, 0, through); err != nil {
		t.Fatal(err)
	}
	page, err = svc.List(context.Background(), admin.Id, true, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Unread != 1 || page.Items[0].ID != newer.ID || page.Items[0].Read {
		t.Fatalf("new event was swallowed by read-all: %+v", page)
	}
	db.Model(&model.ActivityEvent{}).Count(&events)
	if events != ActivityMaxRows {
		t.Fatalf("append exceeded hard cap: %d", events)
	}
}
