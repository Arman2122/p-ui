package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"

	"gorm.io/gorm"
)

type FallbackService struct{}

// The WEB proxy relay behind a fallback. Measured against Xray 26.7.28 and the
// real relay: the relay refuses a stream without a PROXY header (v1 and v2 both
// pass), and a client that negotiates h2 with the master reaches the relay as
// HTTP/2 and gets nothing, so the master may offer http/1.1 alone.
const (
	errFallbackWebXver = "a WEB proxy fallback needs xver 1 or 2: the relay refuses a stream without a PROXY header"
	errFallbackWebDest = "a WEB proxy fallback's destination is the relay's own port, which the panel fills in; leave Dest empty"
	errFallbackWebTLS  = "a WEB proxy fallback needs the master on TLS: the client validates a real certificate, so REALITY cannot front it"
	errFallbackWebALPN = "a WEB proxy fallback needs the master's ALPN to be http/1.1 alone: a client that negotiates h2 reaches the relay as HTTP/2 and gets nothing"
)

// webRelayChild is the shape of an inbound running the WEB relay; the
// fallback reaches the relay's loopback port, not the inbound's own listen.
type webRelayChild struct {
	WebEnable   bool   `json:"webEnable"`
	WebHost     string `json:"webHost"`
	WebBindPort int    `json:"webBindPort"`
}

func webRelayOf(settings string) (webRelayChild, bool) {
	var w webRelayChild
	if err := json.Unmarshal([]byte(settings), &w); err != nil || !w.WebEnable {
		return webRelayChild{}, false
	}
	return w, true
}

// checkWebRelayFront is what a master must be to front the WEB relay.
func checkWebRelayFront(master *model.Inbound) error {
	var stream struct {
		Security string `json:"security"`
		TLS      struct {
			ALPN []string `json:"alpn"`
		} `json:"tlsSettings"`
	}
	_ = json.Unmarshal([]byte(master.StreamSettings), &stream)
	if stream.Security != "tls" {
		return common.NewError(errFallbackWebTLS)
	}
	if len(stream.TLS.ALPN) != 1 || stream.TLS.ALPN[0] != "http/1.1" {
		return common.NewError(errFallbackWebALPN)
	}
	return nil
}

// webRelayChildren returns the master's fallback children that run the relay.
func (s *FallbackService) webRelayChildren(db *gorm.DB, masterId int) ([]model.Inbound, error) {
	var children []model.Inbound
	err := db.Where("id IN (?)", db.Model(&model.InboundFallback{}).Select("child_id").Where("master_id = ?", masterId)).
		Find(&children).Error
	if err != nil {
		return nil, err
	}
	out := children[:0]
	for _, child := range children {
		if _, ok := webRelayOf(child.Settings); ok {
			out = append(out, child)
		}
	}
	return out, nil
}

// CheckChildEdit refuses turning the WEB proxy off on an inbound a fallback
// fronts: the row would then point a PROXY-headed stream at the FakeTLS port.
func (s *FallbackService) CheckChildEdit(childId int, oldSettings, newSettings string) error {
	if _, was := webRelayOf(oldSettings); !was {
		return nil
	}
	if _, still := webRelayOf(newSettings); still {
		return nil
	}
	parent, err := s.GetParentForChild(childId)
	if err != nil || parent == nil {
		return err
	}
	return common.NewError("inbound #", parent.MasterId, " fronts this inbound's WEB proxy through a fallback; detach it before turning the WEB proxy off")
}

// errFallbackHostShape is the one wording for the shape fallbacks need, said
// wherever a configuration would leave them attached but never emitted. VLESS
// encryption is in the list because xray-core refuses that pairing outright:
// `"fallbacks" can not be used together with "decryption"` fails the whole
// config, not just this inbound.
const errFallbackHostShape = "fallbacks need an inbound on raw TCP with TLS or REALITY, and without VLESS encryption"

// errFallbackPathShape mirrors xray-core's own refusal, which fails the WHOLE
// config rather than this inbound: vless.go:185 and trojan.go:175. F326.
const errFallbackPathShape = "a fallback path must be empty or start with \"/\""

// FallbackInput is the payload shape POSTed by the inbound form.
type FallbackInput struct {
	ChildId   int    `json:"childId"`
	Name      string `json:"name"`
	Alpn      string `json:"alpn"`
	Path      string `json:"path"`
	Dest      string `json:"dest"`
	Xver      int    `json:"xver"`
	SortOrder int    `json:"sortOrder"`
}

// GetByMaster returns every fallback rule attached to the master inbound.
func (s *FallbackService) GetByMaster(masterId int) ([]model.InboundFallback, error) {
	var rows []model.InboundFallback
	err := database.GetDB().
		Where("master_id = ?", masterId).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// GetParentForChild finds the first fallback rule that points at childId.
// Used by client-link generation: when a child inbound is attached as a
// fallback, its client links should advertise the master's address+port
// and TLS instead of the child's loopback listen.
func (s *FallbackService) GetParentForChild(childId int) (*model.InboundFallback, error) {
	var row model.InboundFallback
	err := database.GetDB().
		Where("child_id = ?", childId).
		Order("sort_order ASC, id ASC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SetByMaster replaces the master's entire fallback list atomically. A list the
// master cannot host is refused rather than stored: the injection is gated on
// the same capability, so stored-and-never-emitted is the one outcome that
// leaves no trace anywhere.
func (s *FallbackService) SetByMaster(masterId int, items []FallbackInput) error {
	db := database.GetDB()
	if len(items) > 0 {
		var master model.Inbound
		if err := db.First(&master, masterId).Error; err != nil {
			return err
		}
		if !inboundCanHostFallbacks(&master) {
			return common.NewError(errFallbackHostShape)
		}
		if err := s.checkWebRelayItems(db, &master, items); err != nil {
			return err
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("master_id = ?", masterId).Delete(&model.InboundFallback{}).Error; err != nil {
			return err
		}
		for i, c := range items {
			childId := c.ChildId
			if childId == masterId {
				childId = 0
			}
			if childId <= 0 && strings.TrimSpace(c.Dest) == "" {
				continue
			}
			// Trimmed BEFORE validating and stored trimmed, so what the core sees
			// is what was checked. xray tests fb.Path[0] on the raw string, so
			// validating a trimmed copy while storing " /ws" passed the guard and
			// still failed the config.
			path := strings.TrimSpace(c.Path)
			if path != "" && !strings.HasPrefix(path, "/") {
				return common.NewError(errFallbackPathShape, ":", c.Path)
			}
			row := model.InboundFallback{
				MasterId:  masterId,
				ChildId:   childId,
				Name:      c.Name,
				Alpn:      c.Alpn,
				Path:      path,
				Dest:      c.Dest,
				Xver:      c.Xver,
				SortOrder: c.SortOrder,
			}
			if row.SortOrder == 0 {
				row.SortOrder = i
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *FallbackService) BuildFallbacksJSON(tx *gorm.DB, masterId int) ([]map[string]any, error) {
	if tx == nil {
		tx = database.GetDB()
	}
	var rows []model.InboundFallback
	err := tx.Where("master_id = ?", masterId).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	childIds := make([]int, 0, len(rows))
	for i := range rows {
		childIds = append(childIds, rows[i].ChildId)
	}
	var children []model.Inbound
	if err := tx.Where("id IN ?", childIds).Find(&children).Error; err != nil {
		return nil, err
	}
	byId := make(map[int]*model.Inbound, len(children))
	for i := range children {
		byId[children[i].Id] = &children[i]
	}

	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		dest := strings.TrimSpace(r.Dest)
		if dest == "" {
			child, ok := byId[r.ChildId]
			if !ok {
				continue
			}
			listen := strings.TrimSpace(child.Listen)
			if listen == "" || listen == "0.0.0.0" || listen == "::" || listen == "::0" {
				listen = "127.0.0.1"
			}
			dest = fmt.Sprintf("%s:%d", listen, child.Port)
			if web, ok := webRelayOf(child.Settings); ok && web.WebBindPort > 0 {
				dest = fmt.Sprintf("127.0.0.1:%d", web.WebBindPort)
			}
		}
		entry := map[string]any{
			"dest": dest,
		}
		if r.Name != "" {
			entry["name"] = r.Name
		}
		if r.Alpn != "" {
			entry["alpn"] = r.Alpn
		}
		if r.Path != "" {
			entry["path"] = r.Path
		}
		if r.Xver > 0 {
			entry["xver"] = r.Xver
		}
		out = append(out, entry)
	}
	return out, nil
}

// checkWebRelayItems applies the WEB relay rules to the rows being stored: each
// relay child's row and the master fronting it.
func (s *FallbackService) checkWebRelayItems(db *gorm.DB, master *model.Inbound, items []FallbackInput) error {
	ids := make([]int, 0, len(items))
	for _, c := range items {
		if c.ChildId > 0 && c.ChildId != master.Id {
			ids = append(ids, c.ChildId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var children []model.Inbound
	if err := db.Where("id IN ?", ids).Find(&children).Error; err != nil {
		return err
	}
	relay := map[int]bool{}
	for _, child := range children {
		if _, ok := webRelayOf(child.Settings); ok {
			relay[child.Id] = true
		}
	}
	for _, c := range items {
		if !relay[c.ChildId] {
			continue
		}
		if c.Xver != 1 && c.Xver != 2 {
			return common.NewError(errFallbackWebXver)
		}
		if strings.TrimSpace(c.Dest) != "" {
			return common.NewError(errFallbackWebDest)
		}
		if err := checkWebRelayFront(master); err != nil {
			return err
		}
	}
	return nil
}
