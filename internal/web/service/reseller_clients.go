package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

type ResellerInboundView struct {
	ID          int    `json:"id" example:"1"`
	Remark      string `json:"remark" example:"Europe"`
	Protocol    string `json:"protocol" example:"vless"`
	Enable      bool   `json:"enable" example:"true"`
	Clients     int64  `json:"clients" example:"8"`
	TrafficUsed int64  `json:"trafficUsed" example:"1073741824"`
}

type ResellerClientInput struct {
	Email      string `json:"email" example:"customer-001"`
	TotalGB    int64  `json:"totalGB" example:"10737418240"`
	ExpiryTime int64  `json:"expiryTime" example:"1800000000000"`
	LimitIP    int    `json:"limitIp" example:"2"`
	Enable     bool   `json:"enable" example:"true"`
	Comment    string `json:"comment" example:"Monthly subscription"`
	InboundIDs []int  `json:"inboundIds" example:"[1]"`
	PlanID     int    `json:"planId" example:"0"`
}

type ResellerClientView struct {
	Plan        *ClientPlanMetadata  `json:"plan,omitempty" gorm:"-"`
	History     []ClientHistoryPoint `json:"history,omitempty" gorm:"-"`
	ID          int                  `json:"id" example:"1"`
	Email       string               `json:"email" example:"customer-001"`
	SubID       string               `json:"subId" example:"a4c061dd-44c0-411d-91bd-d139eaf581ac"`
	TotalGB     int64                `json:"totalGB" example:"10737418240"`
	ExpiryTime  int64                `json:"expiryTime" example:"1800000000000"`
	LimitIP     int                  `json:"limitIp" example:"2"`
	Enable      bool                 `json:"enable" example:"true"`
	Comment     string               `json:"comment" example:"Monthly subscription"`
	TrafficUsed int64                `json:"trafficUsed" example:"1073741824"`
	LastOnline  int64                `json:"lastOnline" example:"1790000000000"`
	InboundIDs  []int                `json:"inboundIds" gorm:"-" example:"[1]"`
}

type ResellerClientPage struct {
	Items []ResellerClientView `json:"items"`
	Total int64                `json:"total" example:"12"`
}

type ResellerMutationResult struct {
	ClientID int  `json:"clientId" example:"1"`
	Pending  bool `json:"pending" example:"false"`
}

type ResellerSaleDay struct {
	Day   string `json:"day" example:"2026-09-12"`
	Bytes int64  `json:"bytes" example:"10737418240"`
}

type ResellerDashboardView struct {
	Reseller  *ResellerView         `json:"reseller"`
	Online    int64                 `json:"online" example:"3"`
	Active    int64                 `json:"active" example:"10"`
	Attention []ResellerClientView  `json:"attention"`
	Inbounds  []ResellerInboundView `json:"inbounds"`
	// The existing response key holds committed quota reservations, not payment events.
	Sales []ResellerSaleDay `json:"sales"`
}

func (s *ResellerService) Inbounds(user *model.User) ([]ResellerInboundView, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	out := []ResellerInboundView{}
	db := database.GetDB()
	used, usedArgs := UsedBytesExpr(db)
	args := append([]any{user.Id}, usedArgs...)
	args = append(args, user.Id)
	err := db.Table("inbounds i").
		Joins("JOIN reseller_inbound_grants g ON g.inbound_id = i.id AND g.user_id = ?", user.Id).
		Select("i.id, i.remark, i.protocol, i.enable, (SELECT COUNT(*) FROM client_inbounds ci JOIN clients c ON c.id = ci.client_id WHERE ci.inbound_id = i.id AND c.owner_user_id = ?) AS clients, (SELECT COALESCE(SUM(COALESCE("+used+",0)),0) FROM client_inbounds ci JOIN clients c ON c.id = ci.client_id JOIN client_traffics ON client_traffics.email = c.email WHERE ci.inbound_id = i.id AND c.owner_user_id = ?) AS traffic_used", args...).
		Order("i.id").Scan(&out).Error
	return out, err
}

func (s *ResellerService) OwnedClient(user *model.User, id int) (*model.ClientRecord, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	var rec model.ClientRecord
	err := database.GetDB().Where("id = ? AND owner_user_id = ?", id, user.Id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResellerForbidden
	}
	return &rec, err
}

func (s *ResellerService) Clients(user *model.User, ownerID, page, size int, search string, planID int) (*ResellerClientPage, error) {
	if !session.IsAdminUser(user) {
		if err := s.CheckAccess(user); err != nil {
			return nil, err
		}
		if ownerID != user.Id {
			return nil, ErrResellerForbidden
		}
	}
	if ownerID <= 0 {
		return nil, ErrResellerInvalid
	}
	page = max(1, page)
	size = min(200, max(1, size))
	db := database.GetDB()
	q := db.Table("clients c").Where("c.owner_user_id = ?", ownerID)
	if search != "" {
		q = q.Where("c.email ILIKE ? OR c.comment ILIKE ?", "%"+search+"%", "%"+search+"%")
	}
	if planID > 0 {
		q = q.Where("EXISTS (SELECT 1 FROM client_policies cp WHERE cp.email = c.email AND cp.policy_id = ?)", planID)
	}
	out := &ResellerClientPage{Items: []ResellerClientView{}}
	if err := q.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	used, usedArgs := UsedBytesExpr(db)
	err := q.Joins("LEFT JOIN client_traffics ON client_traffics.email = c.email").
		Select("c.id,c.email,c.sub_id,c.total_gb,c.expiry_time,c.limit_ip,c.enable,c.comment,COALESCE("+used+",0) AS traffic_used,COALESCE(client_traffics.last_online,0) AS last_online", usedArgs...).
		Order("c.id DESC").Offset((page - 1) * size).Limit(size).Scan(&out.Items).Error
	if err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return out, nil
	}
	ids := make([]int, 0, len(out.Items))
	byID := make(map[int]*ResellerClientView, len(out.Items))
	for i := range out.Items {
		out.Items[i].InboundIDs = []int{}
		ids = append(ids, out.Items[i].ID)
		byID[out.Items[i].ID] = &out.Items[i]
	}
	var memberships []model.ClientInbound
	if err := db.Table("client_inbounds ci").Select("ci.client_id,ci.inbound_id").
		Joins("JOIN clients c ON c.id = ci.client_id").Where("c.owner_user_id = ? AND c.id IN ?", ownerID, ids).
		Order("ci.inbound_id").Scan(&memberships).Error; err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		if row := byID[membership.ClientId]; row != nil {
			row.InboundIDs = append(row.InboundIDs, membership.InboundId)
		}
	}
	emails := make([]string, 0, len(out.Items))
	for _, row := range out.Items {
		emails = append(emails, row.Email)
	}
	plans, err := readClientPlans(db.Where("EXISTS (SELECT 1 FROM clients owned WHERE owned.email = cp.email AND owned.owner_user_id = ?) AND (p.owner_user_id = ? OR cp.policy_id IS NULL)", ownerID, ownerID), emails)
	if err != nil {
		return nil, err
	}
	histories, err := readClientHistories(db.Where("EXISTS (SELECT 1 FROM clients owned WHERE owned.email = client_traffic_samples.email AND owned.owner_user_id = ?)", ownerID), emails, time.Now())
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		out.Items[i].Plan = plans[out.Items[i].Email]
		out.Items[i].History = histories[out.Items[i].Email]
	}
	return out, nil
}

func validateResellerClient(a *model.ResellerAccount, c *model.Client) error {
	if c.Email == "" || len(c.Email) > 128 || len(c.Comment) > 2048 || c.TotalGB < 0 || c.LimitIP < 0 || c.LimitIP > 10000 || c.Reset != 0 || c.ExpiryTime < 0 {
		return ErrResellerInvalid
	}
	if err := validateClientEmail(c.Email); err != nil {
		return fmt.Errorf("%w: invalid client name", ErrResellerInvalid)
	}
	if c.ExpiryTime > 0 && c.ExpiryTime <= time.Now().UnixMilli() {
		return ErrResellerInvalid
	}
	if a.ExpiresAt > 0 && (c.ExpiryTime == 0 || c.ExpiryTime > a.ExpiresAt) || a.TrafficLimit > 0 && c.TotalGB == 0 {
		return ErrResellerBudget
	}
	return nil
}

func checkResellerBudget(tx *gorm.DB, a *model.ResellerAccount) error {
	if err := tx.Raw("SELECT * FROM reseller_accounts WHERE user_id = ? FOR NO KEY UPDATE", a.UserID).Scan(a).Error; err != nil {
		return err
	}
	if a.Suspended || a.BudgetBlocked || a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().UnixMilli() {
		return ErrResellerBudget
	}
	if a.TrafficLimit > 0 && a.TrafficUsed >= a.TrafficLimit {
		return ErrResellerBudget
	}
	return nil
}

func (s *ResellerService) CreateClient(user *model.User, in ResellerClientInput) (*ResellerMutationResult, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	out := &ResellerMutationResult{}
	err := s.WithAccountLock(user.Id, func() error {
		if err := s.CheckAccess(user); err != nil {
			return err
		}
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
			return err
		}
		c := model.Client{Email: in.Email, TotalGB: in.TotalGB, ExpiryTime: in.ExpiryTime, LimitIP: in.LimitIP, Enable: in.Enable, Comment: in.Comment, SubID: uuid.NewString()}
		if in.PlanID != 0 {
			if !a.AllowPlans {
				return ErrResellerForbidden
			}
			if err := (&PolicyService{}).PreparePlanClient(PlanScope{OwnerUserID: &user.Id}, in.PlanID, &c, &in.InboundIDs); err != nil {
				return err
			}
		}
		if err := validateResellerClient(&a, &c); err != nil {
			return err
		}
		if len(in.InboundIDs) == 0 || len(in.InboundIDs) > 1000 {
			return ErrResellerInvalid
		}
		if err := validateResellerGrants(database.GetDB(), user.Id, in.InboundIDs); err != nil {
			return err
		}
		cs, ibs := &ClientService{}, &InboundService{}
		for _, id := range in.InboundIDs {
			ib, err := ibs.GetInbound(id)
			if err != nil {
				return err
			}
			if err := cs.fillProtocolDefaults(&c, ib); err != nil {
				return err
			}
		}
		rec := c.ToRecord()
		rec.OwnerUserID = &user.Id
		if err := database.GetDB().Transaction(func(tx *gorm.DB) error {
			var n int64
			if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ?", user.Id).Count(&n).Error; err != nil {
				return err
			}
			if a.ClientCap > 0 && n >= int64(a.ClientCap) {
				return ErrResellerBudget
			}
			if err := checkResellerBudget(tx, &a); err != nil {
				return err
			}
			if err := tx.Create(rec).Error; err != nil {
				return err
			}
			if !c.Enable {
				return applyStagedOff(tx, &model.ClientRecord{}, "enable", false, rec.Id)
			}
			return nil
		}); err != nil {
			return err
		}
		out.ClientID = rec.Id
		nr, err := cs.Create(ibs, &ClientCreatePayload{Client: c, InboundIds: in.InboundIDs})
		out.Pending = resellerApplyPending(nr, ibs.AnyNodePending(in.InboundIDs))
		if err != nil {
			return fmt.Errorf("client %d is reserved; provisioning needs correction: %w", rec.Id, err)
		}
		if in.PlanID != 0 {
			return (&PolicyService{}).RecordPreparedPlanAssignment(PlanScope{OwnerUserID: &user.Id}, c.Email, in.PlanID)
		}
		return nil
	})
	return out, err
}

func resellerApplyPending(restart, nodePending bool) bool {
	if restart {
		(&XrayService{}).SetToNeedRestart()
	}
	return restart || nodePending
}

func (s *ResellerService) UpdateClient(user *model.User, id int, in ResellerClientInput) (*ResellerMutationResult, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	out := &ResellerMutationResult{ClientID: id}
	err := s.WithAccountLock(user.Id, func() error {
		rec, err := s.OwnedClient(user, id)
		if err != nil {
			return err
		}
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
			return err
		}
		if in.Email != rec.Email || in.PlanID != 0 {
			return ErrResellerInvalid
		}
		if in.Enable != rec.Enable && !a.AllowSuspend {
			return ErrResellerForbidden
		}
		if (in.ExpiryTime == 0 && rec.ExpiryTime != 0 || in.ExpiryTime > rec.ExpiryTime && rec.ExpiryTime != 0) && !a.AllowExtend {
			return ErrResellerForbidden
		}
		if (in.TotalGB == 0 && rec.TotalGB != 0 || in.TotalGB > rec.TotalGB && rec.TotalGB != 0) && !a.AllowTopUp {
			return ErrResellerForbidden
		}
		c := rec.ToClient()
		c.TotalGB, c.ExpiryTime, c.LimitIP, c.Enable, c.Comment = in.TotalGB, in.ExpiryTime, in.LimitIP, in.Enable, in.Comment
		if err := validateResellerClient(&a, c); err != nil {
			return err
		}
		ids, err := (&ClientService{}).GetInboundIdsForRecord(id)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			ids = in.InboundIDs
		}
		if len(ids) == 0 {
			return ErrResellerInvalid
		}
		if err := validateResellerGrants(database.GetDB(), user.Id, ids); err != nil {
			return err
		}
		if err := database.GetDB().Transaction(func(tx *gorm.DB) error { return checkResellerBudget(tx, &a) }); err != nil {
			return err
		}
		cs, ibs := &ClientService{}, &InboundService{}
		nr, err := cs.Update(ibs, id, *c)
		out.Pending = resellerApplyPending(nr, ibs.AnyNodePending(ids))
		if err != nil {
			return err
		}
		attached, err := cs.GetInboundIdsForRecord(id)
		if err != nil {
			return err
		}
		if len(attached) == 0 {
			nr, err = cs.Attach(ibs, id, ids)
			out.Pending = resellerApplyPending(nr, ibs.AnyNodePending(ids))
		}
		return err
	})
	return out, err
}

func (s *ResellerService) DeleteClient(user *model.User, id int) error {
	if err := s.CheckAccess(user); err != nil {
		return err
	}
	return s.WithAccountLock(user.Id, func() error {
		if _, err := s.OwnedClient(user, id); err != nil {
			return err
		}
		nr, err := (&ClientService{}).Delete(&InboundService{}, id, false)
		resellerApplyPending(nr, false)
		return err
	})
}

func (s *ResellerService) ResetClient(user *model.User, id int) error {
	if err := s.CheckAccess(user); err != nil {
		return err
	}
	return s.WithAccountLock(user.Id, func() error {
		rec, err := s.OwnedClient(user, id)
		if err != nil {
			return err
		}
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
			return err
		}
		if !a.AllowReset {
			return ErrResellerForbidden
		}
		if !rec.Enable {
			traffic, err := (&InboundService{}).GetClientTrafficByEmail(rec.Email)
			if err != nil {
				return err
			}
			if traffic == nil || rec.TotalGB == 0 || traffic.Up+traffic.Down < rec.TotalGB {
				return ErrResellerForbidden
			}
		}
		if err := database.GetDB().Transaction(func(tx *gorm.DB) error { return checkResellerBudget(tx, &a) }); err != nil {
			return err
		}
		nr, err := (&ClientService{}).ResetTrafficByEmail(&InboundService{}, rec.Email)
		resellerApplyPending(nr, false)
		return err
	})
}

func (s *ResellerService) ClientLinks(user *model.User, id int, host string) ([]string, error) {
	rec, err := s.OwnedClient(user, id)
	if err != nil {
		return nil, err
	}
	return (&InboundService{}).GetAllClientLinks(host, rec.Email)
}

func (s *ResellerService) Dashboard(user *model.User) (*ResellerDashboardView, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	v, err := s.Get(user, user.Id)
	if err != nil {
		return nil, err
	}
	out := &ResellerDashboardView{Reseller: v, Online: v.Online, Active: v.Active, Attention: []ResellerClientView{}, Sales: []ResellerSaleDay{}}
	out.Inbounds, err = s.Inbounds(user)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	db := database.GetDB()
	used, usedArgs := UsedBytesExpr(db)
	q := db.Table("clients c").Joins("LEFT JOIN client_traffics ON client_traffics.email = c.email").Where("c.owner_user_id = ?", user.Id)
	if err := q.Session(&gorm.Session{}).Where("(c.expiry_time > 0 AND c.expiry_time < ?) OR (c.total_gb > 0 AND COALESCE("+used+",0) >= c.total_gb * 0.9)", append([]any{now + 14*86400000}, usedArgs...)...).
		Select("c.id,c.email,c.sub_id,c.total_gb,c.expiry_time,c.limit_ip,c.enable,c.comment,COALESCE("+used+",0) AS traffic_used,COALESCE(client_traffics.last_online,0) AS last_online", usedArgs...).Order("c.expiry_time").Limit(50).Scan(&out.Attention).Error; err != nil {
		return nil, err
	}
	err = database.GetDB().WithContext(context.Background()).Model(&model.ResellerAllocation{}).Where("user_id = ? AND created_at >= ?", user.Id, now-30*86400000).
		Select("to_char(to_timestamp(created_at / 1000.0) AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day, SUM(bytes) AS bytes").Group("day").Order("day").Scan(&out.Sales).Error
	return out, err
}
