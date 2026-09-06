package service

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/banset"
	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
The ban actuator's half of the join. internal/banset drops addresses on ports
and knows nothing else; the rows below say whose device, on which inbounds and
why; and every pass projects the rows onto the firewall.
*/

// bansManager writes this host's ban set. One, for shapingManager's reason:
// two writers to one host's firewall disagree.
var bansManager = banset.HostManager()

// UseBanManager swaps the manager the pass converges through, for a test's
// recording plane, and answers the function that puts the previous one back.
func UseBanManager(m *banset.Manager) (restore func()) {
	previous := bansManager
	bansManager = m
	return func() { bansManager = previous }
}

const (
	// AllLocalPorts marks a ban covering every local inbound port, resolved
	// each pass so an inbound added later is inside it.
	AllLocalPorts = "*"

	BanReasonOverCap  = "over-cap"
	BanReasonOperator = "operator"

	// The operator's answer to a source another client under its cap is live
	// from: behind one NAT, that client's address is the abuser's too.
	SharedAddressBan  = "ban"
	SharedAddressSkip = "skip"

	// IPLimitBanMinutesDefault stands in for an unset or zero duration: a form
	// that predates the setting posts zero, and zero must mean neither forever nor never.
	IPLimitBanMinutesDefault = 30
)

/*
RecordBans is the act the verdicts named: one row per (client, source) the
firewall is named for, covering every local enabled inbound the client is on,
until now plus the operator's ban duration. A row that exists is left as it is,
since the deadline releases a ban and not a later pass; so a device still over
its cap is not banned again every pass, and one back after its ban lapsed is.
A source another client under its cap is live from is shared, and the operator
says whether it is banned or only reported. Answers the rows it created.
*/
func (s *PolicyService) RecordBans(ctx context.Context, verdicts []IPLimitVerdict, now time.Time) ([]model.ClientBan, error) {
	settings := &SettingService{}
	minutes, err := settings.GetIpLimitBanMinutes()
	if err != nil {
		return nil, err
	}
	onShared, err := settings.GetIpLimitSharedAddressAction()
	if err != nil {
		return nil, err
	}
	if err := s.pruneExpiredBans(ctx, now); err != nil {
		return nil, err
	}

	holders := make(map[string][]string)
	for _, verdict := range verdicts {
		for _, kept := range verdict.Keep {
			holders[kept.IP] = append(holders[kept.IP], verdict.Email)
		}
	}

	db := database.GetDB().WithContext(ctx)
	until := now.Add(time.Duration(minutes) * time.Minute).Unix()
	var created []model.ClientBan
	for _, verdict := range verdicts {
		if len(verdict.BanPorts) == 0 {
			continue
		}
		for _, over := range verdict.Ban {
			if others := sharedWith(holders[over.IP], verdict.Email); len(others) > 0 && onShared == SharedAddressSkip {
				logger.Infof("[LIMIT_IP] %s is over its cap from %s, which %s hold inside theirs; not banned, as the shared-address setting says",
					verdict.Email, over.IP, strings.Join(others, ", "))
				continue
			}
			row := model.ClientBan{
				ClientEmail: verdict.Email, Source: over.IP, Ports: portsCSV(verdict.BanPorts),
				Reason: BanReasonOverCap, CreatedAt: now.Unix(), Until: until,
			}
			result := db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "client_email"}, {Name: "source"}, {Name: "node_guid"}},
				DoNothing: true,
			}).Create(&row)
			if result.Error != nil {
				return created, result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			created = append(created, row)
			banLog("ban", row)
			logger.Infof("[LIMIT_IP] %s from %s dropped on port(s) %s for %d minute(s)", verdict.Email, over.IP, row.Ports, minutes)
		}
	}
	return created, nil
}

/*
ConvergeBans projects this host's active rows onto its firewall, level-triggered.
Expired rows are pruned first: one past its deadline would have nft reject the
whole set. With banning off, every row this host holds is released and the
table goes with it, so turning the toggle off frees the banned at once.
*/
func (s *PolicyService) ConvergeBans(ctx context.Context, now time.Time) error {
	db := database.GetDB().WithContext(ctx)
	if !config.IPLimitBansEnabled() {
		if err := s.releaseBans(ctx, db.Where("node_guid = ''"), "banning off"); err != nil {
			return err
		}
		return bansManager.Converge(ctx, now, nil)
	}
	if err := s.pruneExpiredBans(ctx, now); err != nil {
		return err
	}
	var rows []model.ClientBan
	if err := db.Where("node_guid = ''").Order("id").Find(&rows).Error; err != nil {
		return err
	}
	entries, err := s.banEntries(rows)
	if err != nil {
		return err
	}
	return bansManager.Converge(ctx, now, entries)
}

// ActiveBans is every ban this host holds, soonest to lapse first.
func (s *PolicyService) ActiveBans(ctx context.Context) ([]model.ClientBan, error) {
	var rows []model.ClientBan
	err := database.GetDB().WithContext(ctx).Where("node_guid = ''").Order("until, id").Find(&rows).Error
	return rows, err
}

// ReleaseBan frees one source for one client on this host now, not at the next
// pass, and answers how many rows it released.
func (s *PolicyService) ReleaseBan(ctx context.Context, email, source string, now time.Time) (int, error) {
	db := database.GetDB().WithContext(ctx)
	scope := db.Where("node_guid = '' AND client_email = ? AND source = ?", email, source)
	released, err := s.releaseCounted(ctx, scope, "operator")
	if err != nil {
		return 0, err
	}
	return released, s.ConvergeBans(ctx, now)
}

// ReleaseAllBans frees every ban this host holds, now.
func (s *PolicyService) ReleaseAllBans(ctx context.Context, now time.Time) (int, error) {
	db := database.GetDB().WithContext(ctx)
	released, err := s.releaseCounted(ctx, db.Where("node_guid = ''"), "operator")
	if err != nil {
		return 0, err
	}
	return released, s.ConvergeBans(ctx, now)
}

/*
BanAddress is an operator's own ban: a source dropped on every local inbound
port, the ports resolved each pass so an inbound added later is inside it, for
the given minutes or the operator's default. It is recorded under no client.
*/
func (s *PolicyService) BanAddress(ctx context.Context, source string, minutes int, now time.Time) (model.ClientBan, error) {
	if _, err := netip.ParseAddr(source); err != nil {
		return model.ClientBan{}, fmt.Errorf("policy: ban %q: %w", source, err)
	}
	if minutes <= 0 {
		var err error
		if minutes, err = (&SettingService{}).GetIpLimitBanMinutes(); err != nil {
			return model.ClientBan{}, err
		}
	}
	row := model.ClientBan{
		Source: source, Ports: AllLocalPorts, Reason: BanReasonOperator,
		CreatedAt: now.Unix(), Until: now.Add(time.Duration(minutes) * time.Minute).Unix(),
	}
	db := database.GetDB().WithContext(ctx)
	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_email"}, {Name: "source"}, {Name: "node_guid"}},
		DoUpdates: clause.AssignmentColumns([]string{"ports", "reason", "created_at", "until"}),
	}).Create(&row).Error
	if err != nil {
		return model.ClientBan{}, err
	}
	banLog("ban", row)
	return row, s.ConvergeBans(ctx, now)
}

func (s *PolicyService) pruneExpiredBans(ctx context.Context, now time.Time) error {
	db := database.GetDB().WithContext(ctx)
	return s.releaseBans(ctx, db.Where("until <= ?", now.Unix()), "expired")
}

func (s *PolicyService) releaseBans(ctx context.Context, scope *gorm.DB, why string) error {
	_, err := s.releaseCounted(ctx, scope, why)
	return err
}

// releaseCounted deletes the rows in scope, logging each, and answers how many.
func (s *PolicyService) releaseCounted(_ context.Context, scope *gorm.DB, why string) (int, error) {
	var rows []model.ClientBan
	if err := scope.Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Id)
	}
	if err := database.GetDB().Delete(&model.ClientBan{}, ids).Error; err != nil {
		return 0, err
	}
	for _, row := range rows {
		banLog("release ("+why+")", row)
	}
	return len(rows), nil
}

// banEntries is the rows as the firewall wants them: one entry per port, the
// "*" rows over every local inbound port as it stands now.
func (s *PolicyService) banEntries(rows []model.ClientBan) ([]banset.Entry, error) {
	var every []uint16
	var entries []banset.Entry
	for _, row := range rows {
		source, err := netip.ParseAddr(row.Source)
		if err != nil {
			logger.Warningf("[LIMIT_IP] ban row %d names %q, which no firewall can drop; skipped", row.Id, row.Source)
			continue
		}
		ports := parsePortsCSV(row.Ports)
		if row.Ports == AllLocalPorts {
			if every == nil {
				if every, err = s.inboundService.localInboundPorts(); err != nil {
					return nil, err
				}
			}
			ports = every
		}
		for _, port := range ports {
			entries = append(entries, banset.Entry{Source: source, Port: port, Until: time.Unix(row.Until, 0)})
		}
	}
	return entries, nil
}

// localBanPorts is where a ban on this client lands here: the port of every
// enabled inbound of this host the client is on, so the device cannot hop to another.
func localBanPorts(hosted []*model.Inbound) []uint16 {
	var ports []uint16
	for _, inbound := range hosted {
		if inbound.NodeID != nil || !inbound.Enable || inbound.Port <= 0 || inbound.Port > 65535 {
			continue
		}
		if port := uint16(inbound.Port); !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	slices.Sort(ports)
	return ports
}

// localInboundPorts is every enabled local inbound's port, for an operator's ban.
func (s *InboundService) localInboundPorts() ([]uint16, error) {
	var raw []int
	err := database.GetDB().Model(&model.Inbound{}).
		Where("node_id IS NULL AND enable = ? AND port > 0 AND port <= 65535", true).
		Distinct().Order("port").Pluck("port", &raw).Error
	if err != nil {
		return nil, err
	}
	ports := make([]uint16, 0, len(raw))
	for _, port := range raw {
		ports = append(ports, uint16(port))
	}
	return ports, nil
}

func sharedWith(holders []string, self string) []string {
	var others []string
	for _, holder := range holders {
		if holder != self && !slices.Contains(others, holder) {
			others = append(others, holder)
		}
	}
	sort.Strings(others)
	return others
}

func portsCSV(ports []uint16) string {
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(int(port)))
	}
	return strings.Join(parts, ",")
}

func parsePortsCSV(csv string) []uint16 {
	var ports []uint16
	for _, part := range strings.Split(csv, ",") {
		if port, err := strconv.ParseUint(strings.TrimSpace(part), 10, 16); err == nil && port > 0 {
			ports = append(ports, uint16(port))
		}
	}
	return ports
}

// banLog is the operator's record of every ban and release, in the file the
// bash menu's ban log and the Telegram report already read.
func banLog(action string, row model.ClientBan) {
	file, err := os.OpenFile(xray.GetIPLimitBannedLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Warning("[LIMIT_IP] ban log:", err)
		return
	}
	defer file.Close()
	who := row.ClientEmail
	if who == "" {
		who = "-"
	}
	fmt.Fprintf(file, "%s %s %s %s ports=%s until=%s reason=%s\n",
		time.Now().UTC().Format(time.RFC3339), action, who, row.Source, row.Ports,
		time.Unix(row.Until, 0).UTC().Format(time.RFC3339), row.Reason)
}
