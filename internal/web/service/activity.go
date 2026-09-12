package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/websocket"
)

const (
	ActivityMaxRows   = 10_000
	ActivityRetention = 30 * 24 * time.Hour
)

type ActivityView struct {
	ID         string            `json:"id" example:"42"`
	Kind       string            `json:"kind" example:"core.crash"`
	Category   string            `json:"category" example:"activity"`
	Level      string            `json:"level" example:"error"`
	Title      string            `json:"title" example:""`
	Message    string            `json:"message" example:""`
	TitleKey   string            `json:"titleKey" example:"events.coreCrash.title"`
	MessageKey string            `json:"messageKey" example:"events.coreCrash.body"`
	Params     map[string]string `json:"params"`
	At         int64             `json:"at" example:"1790000000000"`
	Read       bool              `json:"read" example:"false"`
}

type ActivityPage struct {
	Generation string         `json:"generation" example:"server-generation"`
	Items      []ActivityView `json:"items"`
	Unread     int64          `json:"unread" example:"4"`
	Recent     int64          `json:"recent" example:"12"`
	NextBefore int64          `json:"nextBefore" example:"0"`
}

type ActivityService struct{}

var activityStore struct {
	sync.Mutex
	db         *gorm.DB
	generation string
}

func activityGeneration(db *gorm.DB) string {
	activityStore.Lock()
	defer activityStore.Unlock()
	if activityStore.db != db {
		activityStore.db = db
		activityStore.generation = uuid.NewString()
	}
	return activityStore.generation
}

func activityParams(params map[string]string) string {
	clean := make(map[string]string, len(params))
	for k, v := range params {
		// Raw engine errors may contain upstream credentials; logs own those details.
		if k == "error" || strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") {
			clean[k] = ""
			continue
		}
		if len(v) > 512 {
			v = string([]rune(v)[:min(len([]rune(v)), 128)])
		}
		clean[k] = v
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

func activityView(row model.ActivityEvent, read bool) ActivityView {
	params := map[string]string{}
	_ = json.Unmarshal([]byte(row.Params), &params)
	return ActivityView{
		ID: strconv.FormatInt(row.Id, 10), Kind: row.Kind, Category: "activity", Level: row.Level,
		TitleKey: row.TitleKey, MessageKey: row.MessageKey, Params: params, At: row.CreatedAt, Read: read,
	}
}

func (s ActivityService) Record(ctx context.Context, row model.ActivityEvent, params map[string]string) (*ActivityView, error) {
	db := database.GetDB()
	if db == nil {
		return nil, errors.New("activity database is not initialized")
	}
	row.Id = 0
	row.Params = activityParams(params)
	if row.CreatedAt == 0 {
		row.CreatedAt = time.Now().UnixMilli()
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.activity', 0))").Error; err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return pruneActivity(tx, time.Now())
	})
	if err != nil {
		return nil, err
	}
	view := activityView(row, false)
	// Global sockets are admin-only. Resellers read their own audience by REST.
	websocket.BroadcastActivity(view)
	return &view, nil
}

func pruneActivity(db *gorm.DB, now time.Time) error {
	if err := db.Where("created_at < ?", now.Add(-ActivityRetention).UnixMilli()).Delete(&model.ActivityEvent{}).Error; err != nil {
		return err
	}
	if err := db.Exec(`DELETE FROM activity_events WHERE id IN
		(SELECT id FROM activity_events ORDER BY id DESC OFFSET ?)`, ActivityMaxRows).Error; err != nil {
		return err
	}
	return db.Exec("DELETE FROM activity_reads WHERE event_id NOT IN (SELECT id FROM activity_events)").Error
}

func (s ActivityService) Prune(ctx context.Context, now time.Time) error {
	db := database.GetDB()
	if db == nil {
		return errors.New("activity database is not initialized")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.activity', 0))").Error; err != nil {
			return err
		}
		return pruneActivity(tx, now)
	})
}

func activityAudience(db *gorm.DB, userID int, admin bool) *gorm.DB {
	if admin {
		return db
	}
	return db.Where("audience_user_id = ? AND audience_user_id > 0", userID)
}

func (s ActivityService) List(ctx context.Context, userID int, admin bool, before int64, limit int) (ActivityPage, error) {
	page := ActivityPage{Items: []ActivityView{}}
	if userID <= 0 {
		return page, errors.New("a signed-in user is required")
	}
	limit = max(1, min(limit, 200))
	connection := database.GetDB()
	page.Generation = activityGeneration(connection)
	db := connection.WithContext(ctx)
	q := activityAudience(db.Model(&model.ActivityEvent{}), userID, admin).Where("created_at >= ?", time.Now().Add(-ActivityRetention).UnixMilli())
	if err := q.Session(&gorm.Session{}).Where("id NOT IN (SELECT event_id FROM activity_reads WHERE user_id = ?)", userID).Count(&page.Unread).Error; err != nil {
		return page, err
	}
	if err := q.Session(&gorm.Session{}).Where("created_at >= ?", time.Now().Add(-24*time.Hour).UnixMilli()).Count(&page.Recent).Error; err != nil {
		return page, err
	}
	if before > 0 {
		q = q.Where("id < ?", before)
	}
	var rows []model.ActivityEvent
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return page, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextBefore = rows[len(rows)-1].Id
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Id)
	}
	read := make(map[int64]bool)
	if len(ids) > 0 {
		var marks []model.ActivityRead
		if err := db.Where("user_id = ? AND event_id IN ?", userID, ids).Find(&marks).Error; err != nil {
			return page, err
		}
		for _, mark := range marks {
			read[mark.EventID] = true
		}
	}
	for _, row := range rows {
		page.Items = append(page.Items, activityView(row, read[row.Id]))
	}
	return page, nil
}

func (s ActivityService) MarkRead(ctx context.Context, userID int, admin bool, id, throughID int64) error {
	if userID <= 0 || (id > 0) == (throughID > 0) {
		return errors.New("a user and exactly one positive event id are required")
	}
	visibility := "audience_user_id = ?"
	if admin {
		visibility = "? > 0"
	}
	if id > 0 {
		var exists int64
		if err := database.GetDB().WithContext(ctx).Model(&model.ActivityEvent{}).Where("id = ? AND "+visibility, id, userID).Count(&exists).Error; err != nil {
			return err
		}
		if exists == 0 {
			return errors.New("activity event is not available")
		}
	}
	condition, bound := "id = ?", id
	if throughID > 0 {
		condition, bound = "id <= ?", throughID
	}
	return database.GetDB().WithContext(ctx).Exec(`INSERT INTO activity_reads (user_id,event_id,read_at)
		SELECT ?,id,? FROM activity_events WHERE `+visibility+` AND `+condition+`
		ON CONFLICT (user_id,event_id) DO NOTHING`, userID, time.Now().UnixMilli(), userID, bound).Error
}
