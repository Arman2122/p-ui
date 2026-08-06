package core

import "slices"

/*
The vocabulary a core declares its clients' credential fields in.

Each name is the one the client record already carries the value under
(model.ClientRecord's JSON names, plus the two client_credentials keys), so a
declaration needs no second naming convention and no translation table.

The set is closed. A name outside it renders as nothing, which is an operator
who cannot fill in a field the core needs — so widening it is a UI change and
coretest fails a core that declares one on its own.
*/

const (
	CredUUID         = "uuid"
	CredPassword     = "password"
	CredAuth         = "auth"
	CredSecurity     = "security"
	CredSecret       = "secret"
	CredAdTag        = "adTag"
	CredPrivateKey   = "privateKey"
	CredPublicKey    = "publicKey"
	CredPreSharedKey = "preSharedKey"
	CredAllowedIPs   = "allowedIPs"
)

var clientCredentials = map[string]bool{
	CredUUID:         true,
	CredPassword:     true,
	CredAuth:         true,
	CredSecurity:     true,
	CredSecret:       true,
	CredAdTag:        true,
	CredPrivateKey:   true,
	CredPublicKey:    true,
	CredPreSharedKey: true,
	CredAllowedIPs:   true,
}

// IsClientCredential reports whether name is one the client form can render.
func IsClientCredential(name string) bool { return clientCredentials[name] }

// ClientCredentialNames returns the vocabulary, sorted. openapigen emits it so
// the renderer's literals are checked against this file instead of copied.
func ClientCredentialNames() []string {
	names := make([]string, 0, len(clientCredentials))
	for name := range clientCredentials {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
