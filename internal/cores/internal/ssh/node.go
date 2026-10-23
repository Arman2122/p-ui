package ssh

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
)

type NodeConfiguration struct {
	HostKeyPEM string          `json:"hostKeyPEM"`
	Inbounds   []core.Instance `json:"inbounds"`
}

func ParseNodeConfiguration(data []byte) (NodeConfiguration, error) {
	return ParseNodeConfigurationWithKeys(data, nil)
}

func ParseNodeConfigurationWithKeys(data []byte, defaults func() (string, error)) (NodeConfiguration, error) {
	var result NodeConfiguration
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return result, errors.New("trailing SSH configuration")
	}
	if result.HostKeyPEM == "" && defaults != nil {
		var err error
		result.HostKeyPEM, err = defaults()
		if err != nil {
			return result, err
		}
	}
	if err := ValidateNodeHostKeys(result.HostKeyPEM); err != nil {
		return result, err
	}
	return result, ValidateNodeInbounds(result.Inbounds)
}

func ValidateNodeInbounds(inbounds []core.Instance) error {
	seen := make(map[int]bool)
	for _, inbound := range inbounds {
		if inbound.Kind != Kind || inbound.ID <= 0 || seen[inbound.ID] || inbound.Port < 1 || inbound.Port > 65535 {
			return errors.New("invalid SSH inbound identity or port")
		}
		seen[inbound.ID] = true
		if _, err := parseSettings(inbound.Settings); err != nil {
			return err
		}
		users, emails := make(map[string]bool), make(map[string]bool)
		for _, user := range inbound.Users {
			credential, err := credentialOf(user)
			if err != nil {
				return err
			}
			if users[credential.username] || emails[credential.email] {
				return errors.New("duplicate SSH client identity")
			}
			users[credential.username], emails[credential.email] = true, true
		}
	}
	return nil
}

func ParseNodeSettings(raw string) (Settings, error) { return parseSettings(raw) }

func ValidateNodeHostKeys(material string) error {
	keys := make(map[string]bool)
	rest := []byte(material)
	for len(bytes.TrimSpace(rest)) != 0 {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return errors.New("invalid SSH host key material")
		}
		signer, err := gossh.ParsePrivateKey(pem.EncodeToMemory(block))
		if err != nil || keys[signer.PublicKey().Type()] {
			return errors.New("invalid or repeated SSH host key")
		}
		keys[signer.PublicKey().Type()] = true
		rest = remaining
	}
	if !keys[gossh.KeyAlgoED25519] || !keys[gossh.KeyAlgoRSA] {
		return errors.New("stable Ed25519 and RSA host keys are required")
	}
	return nil
}

func NewNodeHostKeys() (string, error) {
	var keys hostKeySet
	for _, algorithm := range hostKeyAlgos {
		if err := keys.mint(algorithm); err != nil {
			return "", err
		}
	}
	return keys.encode(), nil
}
