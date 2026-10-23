package sshruntime

import (
	"context"

	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type Runtime struct {
	instance  *ssh.Core
	hostKey   string
	directory string
	store     *state.Store
}

func (s *Runtime) SetStateStore(directory string, store *state.Store) {
	s.directory, s.store = directory, store
}

func (s *Runtime) Validate(data []byte) error {
	_, err := ssh.ParseNodeConfigurationWithKeys(data, s.localHostKeys)
	return err
}

func (s *Runtime) Apply(ctx context.Context, data []byte, lookup func(string) (*limits.Account, bool)) error {
	config, err := ssh.ParseNodeConfigurationWithKeys(data, s.localHostKeys)
	if err != nil {
		return err
	}
	if s.instance != nil && s.hostKey != config.HostKeyPEM {
		if err := s.Close(); err != nil {
			return err
		}
	}
	if s.instance == nil {
		s.hostKey = config.HostKeyPEM
		s.instance = ssh.New(ssh.Deps{Governor: lookup, Demand: admission.FromContext(ctx), DemandContext: ctx, LoadHostKey: func() (string, error) { return config.HostKeyPEM, nil }})
	}
	return s.instance.Reconcile(ctx, config.Inbounds)
}

func (s *Runtime) Close() error {
	if s.instance == nil {
		return nil
	}
	err := s.instance.StopAll(context.Background())
	s.instance = nil
	return err
}

func (s *Runtime) InvalidateAuthentication(ctx context.Context, subjects []admission.Subject) error {
	if s.instance == nil {
		return nil
	}
	return s.instance.InvalidateAuthentication(ctx, subjects)
}
