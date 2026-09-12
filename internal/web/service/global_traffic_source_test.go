package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestGlobalTrafficOverlayMatchesQuotaSource(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	now := time.Now().UnixMilli()
	stale := time.Now().Add(-globalTrafficFreshWindow - time.Hour).UnixMilli()
	type source struct {
		master       string
		up, down, at int64
	}
	cases := []struct {
		email              string
		localUp, localDown int64
		globals            []source
		wantUp, wantDown   int64
	}{
		{"opposing masters", 0, 0, []source{{"B", 0, 100, now}, {"A", 100, 0, now}}, 100, 0},
		{"greatest total", 10, 10, []source{{"A", 100, 0, now}, {"B", 5, 120, now - 1000}}, 5, 120},
		{"freshest equal total", 0, 0, []source{{"A", 100, 0, now - 1000}, {"B", 0, 100, now}}, 0, 100},
		{"local tie", 20, 80, []source{{"A", 100, 0, now}}, 20, 80},
		{"local greater", 60, 70, []source{{"A", 100, 0, now}}, 60, 70},
		{"ignore stale", 3, 4, []source{{"A", 10000, 20000, stale}}, 3, 4},
		{"fresh beside stale", 3, 4, []source{{"A", 10000, 20000, stale}, {"B", 5, 40, now}}, 5, 40},
		{"local only", 11, 12, nil, 11, 12},
		{"MixedCase", 2, 3, []source{{"A", 0, 70, now}}, 0, 70},
		{"mixedcase", 20, 30, []source{{"B", 90, 0, now}}, 90, 0},
	}
	locals := make([]xray.ClientTraffic, 0, len(cases))
	var globals []model.ClientGlobalTraffic
	for i, tc := range cases {
		locals = append(locals, xray.ClientTraffic{Email: tc.email, InboundId: i + 1, Enable: true, Up: tc.localUp, Down: tc.localDown, Total: 1000, ExpiryTime: now + 86400000, Reset: 30, LastOnline: now})
		for _, src := range tc.globals {
			globals = append(globals, model.ClientGlobalTraffic{Email: tc.email, MasterGuid: src.master, Up: src.up, Down: src.down, UpdatedAt: src.at})
		}
	}
	if err := db.Create(&locals).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&globals).Error; err != nil {
		t.Fatal(err)
	}
	display := append([]xray.ClientTraffic(nil), locals...)
	overlayGlobalTrafficValues(db, display)
	used, args := UsedBytesExpr(db)
	var enforcement []struct {
		Email string
		Used  int64
	}
	if err := db.Model(&xray.ClientTraffic{}).Select("email, "+used+" AS used", args...).Order("id").Scan(&enforcement).Error; err != nil {
		t.Fatal(err)
	}
	if len(enforcement) != len(cases) {
		t.Fatalf("enforcement rows=%d, want%d", len(enforcement), len(cases))
	}
	for i, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			want := locals[i]
			want.Up, want.Down = tc.wantUp, tc.wantDown
			if display[i] != want {
				t.Fatalf("display=%+v, want same metadata with %d/%d", display[i], tc.wantUp, tc.wantDown)
			}
			if display[i].Up+display[i].Down != enforcement[i].Used || enforcement[i].Email != tc.email {
				t.Fatalf("display total=%d, enforcement=%+v", display[i].Up+display[i].Down, enforcement[i])
			}
		})
	}
	var stored []xray.ClientTraffic
	if err := db.Order("id").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, locals) {
		t.Fatal("display overlay mutated locally measured counters")
	}
}
