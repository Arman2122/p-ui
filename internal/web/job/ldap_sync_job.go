package job

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	ldaputil "github.com/Arman2122/p-ui/internal/util/ldap"
	"github.com/Arman2122/p-ui/internal/web/service"
)

var DefaultTruthyValues = []string{"true", "1", "yes", "on"}

// Share of the previous successful fetch a new one must still return to be
// trusted: a sudden collapse means a broken directory far more often than churn.
const ldapAutoDeleteMinRetainPercent = 50

type LdapSyncJob struct {
	settingService service.SettingService
	inboundService service.InboundService
	clientService  service.ClientService
	xrayService    service.XrayService
	lastFlagCount  atomic.Int64
}

// --- Helper functions for mustGet ---
func mustGetString(fn func() (string, error)) string {
	v, err := fn()
	if err != nil {
		panic(err)
	}
	return v
}

func mustGetInt(fn func() (int, error)) int {
	v, err := fn()
	if err != nil {
		panic(err)
	}
	return v
}

func mustGetBool(fn func() (bool, error)) bool {
	v, err := fn()
	if err != nil {
		panic(err)
	}
	return v
}

func mustGetStringOr(fn func() (string, error), fallback string) string {
	v, err := fn()
	if err != nil || v == "" {
		return fallback
	}
	return v
}

func NewLdapSyncJob() *LdapSyncJob {
	return new(LdapSyncJob)
}

func (j *LdapSyncJob) Run() {
	logger.Info("LDAP sync job started")

	enabled, err := j.settingService.GetLdapEnable()
	if err != nil || !enabled {
		logger.Warning("LDAP disabled or failed to fetch flag")
		return
	}

	// --- LDAP fetch ---
	cfg := ldaputil.Config{
		Host:               mustGetString(j.settingService.GetLdapHost),
		Port:               mustGetInt(j.settingService.GetLdapPort),
		UseTLS:             mustGetBool(j.settingService.GetLdapUseTLS),
		InsecureSkipVerify: mustGetBool(j.settingService.GetLdapInsecureSkipVerify),
		BindDN:             mustGetString(j.settingService.GetLdapBindDN),
		Password:           mustGetString(j.settingService.GetLdapPassword),
		BaseDN:             mustGetString(j.settingService.GetLdapBaseDN),
		UserFilter:         mustGetString(j.settingService.GetLdapUserFilter),
		UserAttr:           mustGetString(j.settingService.GetLdapUserAttr),
		FlagField:          mustGetStringOr(j.settingService.GetLdapFlagField, mustGetString(j.settingService.GetLdapVlessField)),
		TruthyVals:         truthyValuesOrDefault(mustGetString(j.settingService.GetLdapTruthyValues)),
		Invert:             mustGetBool(j.settingService.GetLdapInvertFlag),
	}

	flags, err := ldaputil.FetchVlessFlags(cfg)
	if err != nil {
		logger.Warning("LDAP fetch failed:", err)
		return
	}
	logger.Infof("Fetched %d LDAP flags", len(flags))

	// --- Load all inbounds and all clients once ---
	inboundTags := splitCsv(mustGetString(j.settingService.GetLdapInboundTags))
	inbounds, err := j.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Failed to get inbounds:", err)
		return
	}

	allClients := map[string]*model.Client{}  // email -> client
	inboundMap := map[string]*model.Inbound{} // tag -> inbound
	// Which inbound each identity is actually on. The index above is panel-wide,
	// and enabling from it would reach clients LDAP was never given authority over.
	membership := map[string]map[string]bool{} // tag -> email -> present
	for _, ib := range inbounds {
		inboundMap[ib.Tag] = ib
		clients, err := j.inboundService.GetClients(ib)
		if err != nil {
			// Unreadable settings would otherwise silently narrow LDAP's authority.
			logger.Warningf("LDAP sync: cannot read the clients of inbound %s: %v", ib.Tag, err)
		}
		on := make(map[string]bool, len(clients))
		for i := range clients {
			allClients[clients[i].Email] = &clients[i]
			on[clients[i].Email] = true
		}
		membership[ib.Tag] = on
	}

	// --- Prepare batch operations ---
	autoCreate := mustGetBool(j.settingService.GetLdapAutoCreate)
	defGB := mustGetInt(j.settingService.GetLdapDefaultTotalGB)
	defExpiryDays := mustGetInt(j.settingService.GetLdapDefaultExpiryDays)
	defLimitIP := mustGetInt(j.settingService.GetLdapDefaultLimitIP)

	resolvedInboundIds := make([]int, 0, len(inboundTags))
	resolvedTags := make([]string, 0, len(inboundTags))
	for _, tag := range inboundTags {
		ib := inboundMap[tag]
		if ib == nil {
			logger.Warningf("LDAP inbound tag %s does not match any inbound", tag)
			continue
		}
		resolvedInboundIds = append(resolvedInboundIds, ib.Id)
		resolvedTags = append(resolvedTags, tag)
	}

	plan := planLdapChanges(flags, allClients, membership, resolvedTags)
	clientsToCreate := []model.Client{}
	if autoCreate {
		for _, email := range plan.create {
			clientsToCreate = append(clientsToCreate, j.buildClient(email, defGB, defExpiryDays, defLimitIP))
		}
	}
	clientsToEnable := plan.enable
	clientsToDisable := plan.disable

	j.createClients(clientsToCreate, resolvedInboundIds, resolvedTags)

	// --- Execute enable/disable batch ---
	for tag, emails := range clientsToEnable {
		j.batchSetEnable(inboundMap[tag], emails, true)
	}
	for tag, emails := range clientsToDisable {
		j.batchSetEnable(inboundMap[tag], emails, false)
	}

	// --- Auto delete clients not in LDAP ---
	// Read before the baseline is written, or a first sync judges itself.
	believable := j.fetchLooksComplete(len(flags))
	if mustGetBool(j.settingService.GetLdapAutoDelete) {
		switch {
		case !believable:
			logger.Warningf("LDAP auto-delete skipped: fetched %d users against a previous %d (empty, or below %d%% retention)",
				len(flags), j.lastFlagCount.Load(), ldapAutoDeleteMinRetainPercent)
		case !j.autoDeleteAllowed(len(flags)):
			logger.Warning("LDAP auto-delete skipped: no trusted baseline yet; this sync records one and the next may delete")
		default:
			ldapEmailSet := map[string]struct{}{}
			for e := range flags {
				ldapEmailSet[e] = struct{}{}
			}
			for _, tag := range inboundTags {
				if ib := inboundMap[tag]; ib != nil {
					j.deleteClientsNotInLDAP(ib.Id, ldapEmailSet)
				}
			}
		}
	}
	// Refuses an unbelievable count itself; see recordBaseline.
	j.recordBaseline(len(flags))
}

/*
fetchLooksComplete reports whether this fetch can be believed.

FetchVlessFlags returns (empty, nil) when the bind succeeds but the search
yields nothing — a renamed OU, a lost read grant — which is not "all gone". A
partial answer is the same lie with a smaller number.

Pure, and deliberately separate from autoDeleteAllowed: the answer decides two
different things. A believable fetch becomes the new baseline whether or not
auto-delete is even switched on; only a believable fetch WITH a baseline behind
it may delete.
*/
func (j *LdapSyncJob) fetchLooksComplete(fetched int) bool {
	if fetched == 0 {
		return false
	}
	previous := j.lastFlagCount.Load()
	return previous == 0 || int64(fetched)*100 >= previous*ldapAutoDeleteMinRetainPercent
}

/*
autoDeleteAllowed is the delete consumer, and it needs one thing more.

A first sync has nothing to compare against, so fetchLooksComplete cannot refuse
it — and lastFlagCount lives in memory, so EVERY panel restart is a first sync.
Without this, a restart re-armed mass deletion against whatever the next fetch
happened to return.

So the first believable sync after a start establishes the baseline and deletes
nothing. The second one, judged against it, may delete. That is also the way out
of a legitimate mass off-boarding: restart the panel, let one sync record the
smaller directory, and deletion resumes on the next tick.
*/
/*
recordBaseline is the only writer, and it makes its own judgement.

Deciding believability here rather than at the call site means the baseline
cannot be advanced by a caller who forgot to ask — which is precisely the bug:
the old code stored every successful fetch at the tail of a long function, so a
sync the guard had just refused became the thing the next one was measured
against.
*/
func (j *LdapSyncJob) recordBaseline(fetched int) {
	if !j.fetchLooksComplete(fetched) {
		return
	}
	j.lastFlagCount.Store(int64(fetched))
}

func (j *LdapSyncJob) autoDeleteAllowed(fetched int) bool {
	return j.fetchLooksComplete(fetched) && j.lastFlagCount.Load() > 0
}

func truthyValuesOrDefault(s string) []string {
	if vals := splitCsv(s); len(vals) > 0 {
		return vals
	}
	return DefaultTruthyValues
}

func splitCsv(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// buildClient creates a new client for auto-create; ClientService.Create fills per-protocol credentials
func (j *LdapSyncJob) buildClient(email string, defGB, defExpiryDays, defLimitIP int) model.Client {
	c := model.Client{
		Email:   email,
		Enable:  true,
		LimitIP: defLimitIP,
		TotalGB: int64(defGB) * 1024 * 1024 * 1024,
	}
	if defExpiryDays > 0 {
		c.ExpiryTime = time.Now().Add(time.Duration(defExpiryDays) * 24 * time.Hour).UnixMilli()
	}
	return c
}

// createClients adds each new LDAP client once, attached to every configured inbound
func (j *LdapSyncJob) createClients(newClients []model.Client, inboundIds []int, tags []string) {
	if len(newClients) == 0 || len(inboundIds) == 0 {
		return
	}
	tagList := strings.Join(tags, ",")
	created := 0
	restartNeeded := false
	mine := make([]string, 0, len(newClients))
	for _, c := range newClients {
		nr, err := j.clientService.Create(&j.inboundService, &service.ClientCreatePayload{Client: c, InboundIds: inboundIds})
		if err != nil {
			logger.Warningf("Failed to add client %s for tags %s: %v", c.Email, tagList, err)
			continue
		}
		created++
		mine = append(mine, c.Email)
		if nr {
			restartNeeded = true
		}
	}
	if created == 0 {
		return
	}
	markLdapOrigin(mine)
	logger.Infof("LDAP auto-create: %d clients for %s", created, tagList)
	if restartNeeded {
		j.xrayService.SetToNeedRestart()
	}
}

/*
markLdapOrigin records that the sync created these clients, so the sweep can
tell them from the ones an admin made by hand.

Stamped HERE and not where the row is written: SyncInbound is the one path that
creates a clients row and it serves every caller, so threading an origin through
it would tag admin-created rows too. This is the same shape as the node-snapshot
sweep owning sync_orphaned_at -- the job that owns the lifecycle owns the marker.

`origin = ”` is the whole guard. ClientService.Create REUSES an existing
identity when the email is already taken, so a hand-made client re-added to an
LDAP inbound comes back through here -- and converting it to LDAP-owned would be
adoption, which is not what was asked for. F372.
*/
func markLdapOrigin(emails []string) {
	if len(emails) == 0 {
		return
	}
	db := database.GetDB()
	if db == nil {
		return
	}
	for _, batch := range chunkStrings(emails, ldapSQLInChunk) {
		if err := db.Model(&model.ClientRecord{}).
			Where("email IN ? AND origin = ?", batch, "").
			Update("origin", model.ClientOriginLDAP).Error; err != nil {
			// Not fatal: an unstamped client is one the sweep will refuse to
			// touch, which is the safe direction.
			logger.Warning("LDAP sync: could not record which clients it created:", err)
			return
		}
	}
}

// ldapSQLInChunk keeps an IN list off the driver's parameter ceiling, the same
// bound every other batched query in the panel uses.
const ldapSQLInChunk = 400

func chunkStrings(all []string, size int) [][]string {
	if size <= 0 || len(all) <= size {
		return [][]string{all}
	}
	out := make([][]string, 0, (len(all)+size-1)/size)
	for start := 0; start < len(all); start += size {
		out = append(out, all[start:min(start+size, len(all))])
	}
	return out
}

func (j *LdapSyncJob) batchSetEnable(ib *model.Inbound, emails []string, enable bool) {
	if len(emails) == 0 {
		return
	}
	restartNeeded := false
	changed := 0
	for _, email := range emails {
		ok, needRestart, err := j.clientService.SetClientEnableByEmail(&j.inboundService, email, enable)
		if err != nil {
			logger.Warningf("Batch set enable failed for %s (batched from inbound %s; the change spans every inbound carrying the email): %v", email, ib.Tag, err)
			continue
		}
		if ok {
			changed++
		}
		if needRestart {
			restartNeeded = true
		}
	}
	if changed > 0 {
		logger.Infof("Batch set enable=%v for %d clients batched from inbound %s (each change spans every inbound carrying the email)", enable, changed, ib.Tag)
	}
	if restartNeeded {
		j.xrayService.SetToNeedRestart()
	}
}

// deleteClientsNotInLDAP deletes clients not in LDAP using batches and a single restart
func (j *LdapSyncJob) deleteClientsNotInLDAP(inboundId int, ldapEmails map[string]struct{}) {
	// Re-read the one inbound: the sync added clients to it moments ago and the
	// row the caller loaded before the run no longer lists them. F379.
	ib, err := j.inboundService.GetInbound(inboundId)
	if err != nil {
		logger.Warningf("Failed to load inbound %d for deletion: %v", inboundId, err)
		return
	}
	batchSize := 50 //  clients in 1 batch
	restartNeeded := false

	{
		clients, err := j.inboundService.GetClients(ib)
		if err != nil {
			logger.Warningf("Failed to get clients for inbound %s: %v", ib.Tag, err)
			return
		}

		// Collect clients for deletion
		toDelete := []model.Client{}
		for _, c := range clients {
			if _, ok := ldapEmails[c.Email]; !ok {
				toDelete = append(toDelete, c)
			}
		}

		/*
			And keep the ones LDAP did not create.

			The set above is "every client on this inbound whose email the
			directory did not return", which is every hand-made account on an
			LDAP-managed inbound: a contractor, a founder's own client, a test
			identity. Off-boarding was detaching all of them from every configured
			inbound on the first sweep -- silently, since the log line reads the
			same as an intended removal.

			Only a client this job created is the sweep's to take. Everything
			else is named in the log so an operator watching an off-boarding can
			see why an account survived it. F372.
		*/
		toDelete = j.ldapCreatedOnly(ib.Tag, toDelete)

		if len(toDelete) == 0 {
			return
		}

		for i := 0; i < len(toDelete); i += batchSize {
			end := min(i+batchSize, len(toDelete))
			batch := toDelete[i:end]

			for _, c := range batch {
				nr, err := j.clientService.DetachByEmail(&j.inboundService, ib.Id, c.Email)
				if err != nil {
					logger.Warningf("Failed to delete client %s from inbound id=%d(tag=%s): %v",
						c.Email, ib.Id, ib.Tag, err)
					continue
				}
				logger.Infof("Deleted client %s from inbound id=%d(tag=%s)",
					c.Email, ib.Id, ib.Tag)
				if nr {
					restartNeeded = true
				}
			}
		}
	}

	if restartNeeded {
		j.xrayService.SetToNeedRestart()
		logger.Info("Xray restart scheduled after batch deletion")
	}
}

/*
ldapCreatedOnly narrows a candidate set to the clients this job created.

Fails CLOSED, which is the opposite of most guards here and deliberate: a
question that could not be answered must not authorise a detach. An unreadable
table means nothing is swept this pass and the next one re-decides, where the
other direction would detach every hand-made account on the inbound.
*/
func (j *LdapSyncJob) ldapCreatedOnly(tag string, candidates []model.Client) []model.Client {
	if len(candidates) == 0 {
		return nil
	}
	db := database.GetDB()
	if db == nil {
		return nil
	}
	emails := make([]string, 0, len(candidates))
	for _, c := range candidates {
		emails = append(emails, c.Email)
	}
	mine := make(map[string]struct{}, len(emails))
	for _, batch := range chunkStrings(emails, ldapSQLInChunk) {
		var found []string
		if err := db.Model(&model.ClientRecord{}).
			Where("email IN ? AND origin = ?", batch, model.ClientOriginLDAP).
			Pluck("email", &found).Error; err != nil {
			logger.Warningf("LDAP auto-delete skipped for %s: cannot tell which clients the sync created: %v", tag, err)
			return nil
		}
		for _, email := range found {
			mine[email] = struct{}{}
		}
	}
	out := make([]model.Client, 0, len(candidates))
	kept := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := mine[c.Email]; ok {
			out = append(out, c)
			continue
		}
		kept = append(kept, c.Email)
	}
	if len(kept) > 0 {
		logger.Infof("LDAP auto-delete kept %d client(s) on %s that the sync did not create: %s",
			len(kept), tag, strings.Join(kept, ", "))
	}
	return out
}

// ldapChanges is one tick's decision, with no database or directory in it so
// it can be driven by a test; Run cannot be, because it needs a live directory.
type ldapChanges struct {
	create  []string
	enable  map[string][]string
	disable map[string][]string
}

/*
planLdapChanges decides who to create, enable and disable for one tick.

The ENABLE direction is bounded by membership: a client is only re-enabled on
an inbound it is actually on. Without that, an identity the directory allows
was enabled on every configured tag no matter where it lived, so an admin
disabling a client on an inbound LDAP does not govern had it turned back on a
minute later, forever, with no workaround.

The DISABLE direction stays panel-wide on purpose. Narrowing it fails OPEN:
an off-boarded account would keep working wherever it sits outside the
configured tags.

That was a scope question rather than a repair, and it is settled: the operator
granted LDAP authority to disable, panel-wide, when they answered Q2. Deleting
is the direction that got a limit instead -- only clients the sync created, see
ldapCreatedOnly -- because disabling an account an admin made by hand is
reversible in one click and detaching it from every inbound is not. F369 / F372.
*/
func planLdapChanges(
	flags map[string]bool,
	allClients map[string]*model.Client,
	membership map[string]map[string]bool,
	resolvedTags []string,
) ldapChanges {
	out := ldapChanges{enable: map[string][]string{}, disable: map[string][]string{}}
	for email, allowed := range flags {
		existing := allClients[email]
		if existing == nil {
			if allowed {
				out.create = append(out.create, email)
			}
			continue
		}
		for _, tag := range resolvedTags {
			switch {
			case allowed && !existing.Enable && membership[tag][email]:
				out.enable[tag] = append(out.enable[tag], email)
			case !allowed && existing.Enable:
				out.disable[tag] = append(out.disable[tag], email)
			}
		}
	}
	return out
}
