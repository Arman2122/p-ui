package worker

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type Engine interface {
	Validate([]byte) error
	Apply(context.Context, []byte, func(string) (*limits.Account, bool)) error
	Close() error
}

type AuthenticationInvalidator interface {
	InvalidateAuthentication(context.Context, []admission.Subject) error
}

func (s *streamBackend) AuthorityJournalID() string { return s.governor.JournalID() }

func (s *streamBackend) ApplyCredit(ctx context.Context, credits []authority.Credit) error {
	return s.governor.ApplyCredit(ctx, credits)
}

func (s *streamBackend) Authority(ctx context.Context, after string, limit int) (authority.Page, error) {
	return s.governor.Authority(ctx, after, limit)
}

type Backend interface {
	Validate(*configuration.Runtime) error
	Apply(context.Context, *configuration.Runtime) error
	Collect(context.Context) error
	Shutdown(context.Context) error
}

type streamBackend struct {
	id          string
	incarnation string
	engine      Engine
	store       *state.Store
	governor    *governor.Store
	last        map[string][2]uint64
	admissions  []admission.Descriptor
}

func (s *streamBackend) SetClock(clock limits.Clock) { s.governor.SetClock(clock) }

func NewStreamBackend(id string, engine Engine, store *state.Store, credit *governor.Store) (Backend, error) {
	if engine == nil || store == nil || credit == nil {
		return nil, configuration.ErrConfiguration
	}
	if err := store.BindRuntimeJournal(id, credit.JournalID()); err != nil {
		return nil, err
	}
	return &streamBackend{id: id, incarnation: uuid.NewString(), engine: engine, store: store, governor: credit, last: make(map[string][2]uint64)}, nil
}

func (s *streamBackend) Validate(runtime *configuration.Runtime) error {
	if runtime == nil {
		return nil
	}
	return s.engine.Validate(runtime.Configuration)
}

func (s *streamBackend) Apply(ctx context.Context, runtime *configuration.Runtime) error {
	if runtime == nil {
		if err := s.governor.Reconcile(nil); err != nil {
			return err
		}
		if err := s.engine.Close(); err != nil {
			return err
		}
		s.admissions = nil
		return nil
	}
	descriptors := slices.Clone(runtime.Admissions)
	sort.Slice(descriptors, func(i, j int) bool {
		if descriptors[i].InboundTag != descriptors[j].InboundTag {
			return descriptors[i].InboundTag < descriptors[j].InboundTag
		}
		return descriptors[i].ClientID < descriptors[j].ClientID
	})
	if !slices.Equal(descriptors, s.admissions) {
		var err error
		if engine, ok := s.engine.(AuthenticationInvalidator); ok {
			err = engine.InvalidateAuthentication(ctx, changedSubjects(s.admissions, descriptors))
		} else {
			err = s.engine.Close()
		}
		if err != nil {
			return err
		}
	}
	if err := s.governor.Reconcile(runtime.Grants); err != nil {
		return err
	}
	if err := s.engine.Apply(ctx, runtime.Configuration, s.governor.Lookup); err != nil {
		return err
	}
	s.admissions = descriptors
	return nil
}

func changedSubjects(before, after []admission.Descriptor) []admission.Subject {
	previous := make(map[admission.Subject]admission.Descriptor, len(before))
	for _, descriptor := range before {
		previous[admission.Subject{ClientID: descriptor.ClientID, InboundTag: descriptor.InboundTag}] = descriptor
	}
	var changed []admission.Subject
	for _, descriptor := range after {
		subject := admission.Subject{ClientID: descriptor.ClientID, InboundTag: descriptor.InboundTag}
		if old, ok := previous[subject]; !ok || old != descriptor {
			changed = append(changed, subject)
		}
		delete(previous, subject)
	}
	for subject := range previous {
		changed = append(changed, subject)
	}
	return changed
}

func (s *streamBackend) Shutdown(ctx context.Context) error {
	return errors.Join(s.engine.Close(), s.Collect(ctx))
}

func (w *streamBackend) Collect(context.Context) error {
	observations, err := w.governor.Observe()
	if err != nil {
		return err
	}
	var checkpoints []*nodev1.UsageCheckpoint
	for _, observation := range observations {
		grant, snapshot := observation.Grant, observation.Snapshot
		values := [2]uint64{snapshot.MeasuredUpload, snapshot.MeasuredDownload}
		if w.last[grant.ID] == values {
			continue
		}
		checkpoints = append(checkpoints, &nodev1.UsageCheckpoint{
			GrantId: grant.ID, ClientId: grant.ClientID,
			TermId: grant.Policy.Term, MeterId: w.id, MeterIncarnation: w.incarnation,
			Uploaded: values[0], Downloaded: values[1], ObservedUnixMilli: time.Now().UnixMilli(),
		})
	}
	for len(checkpoints) != 0 {
		n := min(1024, len(checkpoints))
		batch := checkpoints[:n]
		if _, err := w.store.AppendUsage(batch); err != nil {
			return err
		}
		for _, checkpoint := range batch {
			w.last[checkpoint.GrantId] = [2]uint64{checkpoint.Uploaded, checkpoint.Downloaded}
		}
		checkpoints = checkpoints[n:]
	}
	if health, ok := w.engine.(interface{ Check() error }); ok {
		return health.Check()
	}
	return nil
}
