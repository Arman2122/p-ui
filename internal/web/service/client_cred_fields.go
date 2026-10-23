package service

import (
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// The vocabulary uses uuid where settings use id; list values use comma-separated
// prefixes so source validation and per-binding projection share one conversion.
func clientCredentialValues(c *model.Client) map[string]string {
	return map[string]string{
		core.CredUUID:         c.ID,
		core.CredPassword:     c.Password,
		core.CredUsername:     c.Username,
		core.CredTLSCryptV2:   c.TLSCryptV2,
		core.CredAuth:         c.Auth,
		core.CredSecurity:     c.Security,
		core.CredSecret:       c.Secret,
		core.CredAdTag:        c.AdTag,
		core.CredPrivateKey:   c.PrivateKey,
		core.CredPublicKey:    c.PublicKey,
		core.CredPreSharedKey: c.PSK(),
		core.CredAllowedIPs:   strings.Join(c.AllowedIPs, ","),
	}
}

func setClientCredential(c *model.Client, name, value string) {
	switch name {
	case core.CredUUID:
		c.ID = value
	case core.CredPassword:
		c.Password = value
	case core.CredUsername:
		c.Username = value
	case core.CredTLSCryptV2:
		c.TLSCryptV2 = value
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
		c.PreSharedKey = &value
	case core.CredAllowedIPs:
		c.AllowedIPs = nil
		if value != "" {
			c.AllowedIPs = strings.Split(value, ",")
			for i := range c.AllowedIPs {
				c.AllowedIPs[i] = strings.TrimSpace(c.AllowedIPs[i])
			}
		}
	}
}

// inboundBoundCredentials is what the operator submitted for the credentials
// each inbound mints for itself, restored before every inbound's mint.
func inboundBoundCredentials(c *model.Client) map[string]string {
	out := map[string]string{}
	for name, value := range clientCredentialValues(c) {
		if core.InboundBound(name) {
			out[name] = value
		}
	}
	return out
}

func setClientCredentials(c *model.Client, values map[string]string) {
	for name, value := range values {
		setClientCredential(c, name, value)
	}
}

// restoreInboundBound fills what the caller omitted from the inbound's own
// stored rows, so a partial edit keeps each inbound's key instead of minting one.
func restoreInboundBound(c *model.Client, clientId, inboundId int) error {
	stored, err := storedCredentialsFor(nil, clientId, inboundId)
	if err != nil {
		return err
	}
	for name, value := range clientCredentialValues(c) {
		if core.InboundBound(name) && value == "" {
			setClientCredential(c, name, stored[name])
		}
	}
	return nil
}

// clientIdentityValue reads the field a core named as this kind's config
// identity — a vocabulary credential, or the email every client carries.
// identityFieldFor names the field the kind's core keys its user table on.
func identityFieldFor(p model.Protocol) string {
	if a, ok := cores.ClientCredentialAuthority(core.Kind(p)); ok {
		return a.ClientIdentity(core.Kind(p))
	}
	return core.CredUUID
}

// identityCollision names the other client already carrying this identity. The
// core keys its user map on it, so a second claimant silently replaces the first.
func identityCollision(p model.Protocol, value string, others []model.Client, skip int) (string, string) {
	field := identityFieldFor(p)
	// email is already globally unique; the kinds that answer it are out of scope.
	if field == "email" {
		return "", ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	// Byte-exact and case-SENSITIVE on purpose: trojan keys on sha224 of the
	// raw password, so folding case here would over-refuse.
	for i := range others {
		if i == skip {
			continue
		}
		if strings.TrimSpace(clientIdentityValue(&others[i], field)) == value {
			return others[i].Email, field
		}
	}
	return "", ""
}

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
