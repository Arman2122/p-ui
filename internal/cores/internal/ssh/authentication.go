package ssh

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/admission"
)

type authentication struct {
	credential
	token   string
	proof   *admission.BoundProof
	scoped  bool
	revoked atomic.Bool
}

func (a *authentication) current() bool {
	return a != nil && !a.revoked.Load() && (!a.scoped || a.proof.Current())
}

func (s *server) setUsers(users []core.User) error {
	return s.setAuthentication(users, nil, s.tag)
}

func (s *server) setAuthentication(users []core.User, binding *admission.Binding, tag string) error {
	next, nextEmail := make(map[string]*authentication, len(users)), make(map[string]*authentication, len(users))
	for _, user := range users {
		credential, err := credentialOf(user)
		if err != nil {
			return err
		}
		if next[credential.username] != nil || nextEmail[credential.email] != nil {
			return errors.New("ssh: duplicate client credential identity")
		}
		proof, found := binding.Capture(credential.email, tag)
		if binding != nil && !found {
			return admission.ErrUnavailable
		}
		record := &authentication{credential: credential, proof: proof, scoped: binding != nil}
		next[credential.username], nextEmail[credential.email] = record, record
	}
	s.mu.Lock()
	for name, desired := range next {
		old := s.byUser[name]
		if old != nil && old.credential == desired.credential && old.scoped == desired.scoped && old.proof == desired.proof && !old.revoked.Load() {
			next[name], nextEmail[old.email] = old, old
		} else {
			desired.token = uuid.NewString()
		}
	}
	for name, old := range s.byUser {
		if next[name] != old {
			old.revoked.Store(true)
		}
	}
	s.byUser, s.byEmail = next, nextEmail
	var doomed []*conn
	for connection := range s.conns {
		record := nextEmail[connection.email]
		if record == nil || !record.enable || connection.authentication != nil && connection.authentication != record {
			doomed = append(doomed, connection)
		}
	}
	s.mu.Unlock()
	for _, connection := range doomed {
		connection.close()
	}
	return nil
}

func (s *server) invalidateAuthentication(subjects map[admission.Subject]bool) {
	s.mu.Lock()
	var doomed []*conn
	for _, record := range s.byUser {
		tag := s.tag
		if record.proof != nil {
			tag = record.proof.Descriptor().InboundTag
		}
		if subjects[admission.Subject{ClientID: record.email, InboundTag: tag}] {
			record.revoked.Store(true)
		}
	}
	for connection := range s.conns {
		if connection.authentication != nil && connection.authentication.revoked.Load() {
			doomed = append(doomed, connection)
		}
	}
	s.mu.Unlock()
	for _, connection := range doomed {
		connection.close()
	}
}

func (c *Core) InvalidateAuthentication(ctx context.Context, subjects []admission.Subject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	changed := make(map[admission.Subject]bool, len(subjects))
	for _, subject := range subjects {
		changed[subject] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, instance := range c.instances {
		instance.srv.invalidateAuthentication(changed)
	}
	return nil
}
