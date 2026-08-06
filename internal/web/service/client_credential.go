package service

import (
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

/*
readClientCredentials returns credential rows filtered by client and/or inbound;
a nil clientIds or a zero inboundId means "any". The join hides rows whose
inbound the client left, so detaching never has to prune them — and re-attaching
gets the old value back, which is why Attach reads past the join instead.
*/
func readClientCredentials(tx *gorm.DB, clientIds []int, inboundId int) ([]model.ClientCredential, error) {
	if tx == nil {
		tx = database.GetDB()
	}
	q := tx.Table("client_credentials cc").
		Select("cc.client_id, cc.inbound_id, cc.key, cc.value").
		Joins("JOIN client_inbounds ci ON ci.client_id = cc.client_id AND ci.inbound_id = cc.inbound_id")
	if clientIds != nil {
		if len(clientIds) == 0 {
			return nil, nil
		}
		q = q.Where("cc.client_id IN ?", clientIds)
	}
	if inboundId > 0 {
		q = q.Where("cc.inbound_id = ?", inboundId)
	}
	var rows []model.ClientCredential
	if err := q.Order("cc.client_id ASC, cc.inbound_id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// credentialsByClient collapses rows to clientId → key → value; the lowest
// inbound id wins, standing in for the single column these rows replaced.
func credentialsByClient(rows []model.ClientCredential) map[int]map[string]string {
	out := make(map[int]map[string]string, len(rows))
	for _, r := range rows {
		byKey, ok := out[r.ClientId]
		if !ok {
			byKey = map[string]string{}
			out[r.ClientId] = byKey
		}
		if _, seen := byKey[r.Key]; !seen {
			byKey[r.Key] = r.Value
		}
	}
	return out
}

// CredentialsForRecord returns the credentials the client editor round-trips,
// which the clients table no longer carries as columns.
func (s *ClientService) CredentialsForRecord(clientId int) (map[string]string, error) {
	stored, err := storedClientCredentials(nil, clientId)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		stored = map[string]string{}
	}
	return stored, nil
}

// storedClientCredentials returns one client's credentials as key → value.
func storedClientCredentials(tx *gorm.DB, clientId int) (map[string]string, error) {
	rows, err := readClientCredentials(tx, []int{clientId}, 0)
	if err != nil {
		return nil, err
	}
	return credentialsByClient(rows)[clientId], nil
}

// storedCredentialsFor reads one inbound's rows unjoined when inboundId is set,
// so a detached inbound's value is still visible; otherwise the attached set.
func storedCredentialsFor(tx *gorm.DB, clientId, inboundId int) (map[string]string, error) {
	if inboundId <= 0 {
		return storedClientCredentials(tx, clientId)
	}
	if tx == nil {
		tx = database.GetDB()
	}
	var rows []model.ClientCredential
	if err := tx.Model(&model.ClientCredential{}).
		Where("client_id = ? AND inbound_id = ?", clientId, inboundId).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

/*
hydrateClientCredentials fills a payload the panel built for itself out of a
stored record, which ToClient can no longer carry now that these fields have
left the clients row.

For internally synthesised payloads only. The editor's payload is complete by
construction, so an empty field there means the admin cleared it, and hydrating
that would make clearing an ad tag impossible. Without this a traffic reset —
which re-enables a client by round-tripping its own record — looked exactly like
an admin blanking the field and destroyed the credential.

A non-zero inboundId reads that inbound's rows directly, past the attachment
join, because Attach runs before the client is attached and must recover the
value a previous detach left behind rather than minting a fresh one.
*/
func hydrateClientCredentials(tx *gorm.DB, clientId, inboundId int, c *model.Client) error {
	if c == nil || (c.Secret != "" && c.AdTag != "") {
		return nil
	}
	stored, err := storedCredentialsFor(tx, clientId, inboundId)
	if err != nil {
		return err
	}
	if c.Secret == "" {
		c.Secret = stored[model.CredentialSecret]
	}
	if c.AdTag == "" {
		c.AdTag = stored[model.CredentialAdTag]
	}
	return nil
}

// upsertClientCredentials writes rows in one statement, replacing the stored
// value for any key they carry. Clearing a credential is a delete, not an upsert.
func upsertClientCredentials(tx *gorm.DB, rows []model.ClientCredential) error {
	if len(rows) == 0 {
		return nil
	}
	if tx == nil {
		tx = database.GetDB()
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}, {Name: "inbound_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).CreateInBatches(rows, 200).Error
}

// deleteClientCredentials drops the given clients' rows, narrowed to keys when
// any are named.
func deleteClientCredentials(tx *gorm.DB, clientIds []int, keys ...string) error {
	if len(clientIds) == 0 {
		return nil
	}
	if tx == nil {
		tx = database.GetDB()
	}
	q := tx.Where("client_id IN ?", clientIds)
	if len(keys) > 0 {
		q = q.Where("key IN ?", keys)
	}
	return q.Delete(&model.ClientCredential{}).Error
}

// mtprotoCredentialRows projects a client's MTProto credentials onto rows for one
// inbound. Empty is omitted, so a blob without them cannot clear an earlier save.
func mtprotoCredentialRows(clientId, inboundId int, c *model.Client) []model.ClientCredential {
	rows := make([]model.ClientCredential, 0, 2)
	for _, pair := range [][2]string{
		{model.CredentialSecret, c.Secret},
		{model.CredentialAdTag, c.AdTag},
	} {
		if pair[1] == "" {
			continue
		}
		rows = append(rows, model.ClientCredential{
			ClientId: clientId, InboundId: inboundId, Key: pair[0], Value: pair[1],
		})
	}
	return rows
}

// applyMtprotoCredentials fills a client's MTProto fields from stored rows.
func applyMtprotoCredentials(c *model.Client, byKey map[string]string) {
	c.Secret = byKey[model.CredentialSecret]
	c.AdTag = byKey[model.CredentialAdTag]
}
