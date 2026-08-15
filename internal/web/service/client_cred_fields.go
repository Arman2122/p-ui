package service

import (
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

/*
The one table joining the credential vocabulary to model.Client's fields.

Vocabulary names are the client RECORD's JSON names, which diverge from the
settings-JSON in exactly one place: the vocabulary says "uuid", the settings
blob says "id". Nothing reads uuid out of a settings-derived Credentials map
today, so the divergence is named here and nowhere else. allowedIPs is absent
on purpose — it is a list, and no mint, validation or identity reads it.
*/
func clientCredentialValues(c *model.Client) map[string]string {
	return map[string]string{
		core.CredUUID:         c.ID,
		core.CredPassword:     c.Password,
		core.CredAuth:         c.Auth,
		core.CredSecurity:     c.Security,
		core.CredSecret:       c.Secret,
		core.CredAdTag:        c.AdTag,
		core.CredPrivateKey:   c.PrivateKey,
		core.CredPublicKey:    c.PublicKey,
		core.CredPreSharedKey: c.PreSharedKey,
	}
}

func setClientCredential(c *model.Client, name, value string) {
	switch name {
	case core.CredUUID:
		c.ID = value
	case core.CredPassword:
		c.Password = value
	case core.CredAuth:
		c.Auth = value
	case core.CredSecurity:
		c.Security = value
	case core.CredSecret:
		c.Secret = value
	case core.CredAdTag:
		c.AdTag = value
	case core.CredPrivateKey:
		c.PrivateKey = value
	case core.CredPublicKey:
		c.PublicKey = value
	case core.CredPreSharedKey:
		c.PreSharedKey = value
	}
}

// clientIdentityValue reads the field a core named as this kind's config
// identity — a vocabulary credential, or the email every client carries.
func clientIdentityValue(c *model.Client, name string) string {
	if name == "email" {
		return c.Email
	}
	return clientCredentialValues(c)[name]
}

/*
validateClientForKind refuses a client its kind cannot serve, with the core's
own wording. A kind no core owns keeps the oldest rule — a non-empty ID — so a
quarantined inbound's clients are neither loosened nor newly refused.
*/
func validateClientForKind(protocol model.Protocol, settings string, client *model.Client) error {
	kind := core.Kind(protocol)
	authority, ok := cores.ClientCredentialAuthority(kind)
	if !ok {
		if client.ID == "" {
			return common.NewError("empty client ID")
		}
		return nil
	}
	return authority.ValidateClient(kind, settings, client.Email, clientCredentialValues(client))
}
