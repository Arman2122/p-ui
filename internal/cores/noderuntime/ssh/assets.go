package sshruntime

import (
	"crypto/tls"
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func (s *Runtime) ResolveFileAssets(data []byte, resolve configuration.FileAssetResolver) ([]byte, error) {
	config, err := ssh.ParseNodeConfigurationWithKeys(data, s.localHostKeys)
	if err != nil {
		return nil, err
	}
	for i := range config.Inbounds {
		inbound := &config.Inbounds[i]
		settings, err := ssh.ParseNodeSettings(inbound.Settings)
		if err != nil {
			return nil, err
		}
		if settings.Transport != ssh.TransportWSS {
			if settings.CertFile != "" || settings.KeyFile != "" {
				return nil, configuration.ErrConfiguration
			}
			continue
		}
		settings.CertFile, err = resolve(settings.CertFile)
		if err != nil {
			return nil, err
		}
		settings.KeyFile, err = resolve(settings.KeyFile)
		if err != nil {
			return nil, err
		}
		if _, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return nil, err
		}
		inbound.Settings = string(encoded)
	}
	return json.Marshal(config)
}
