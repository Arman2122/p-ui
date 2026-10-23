package main

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type guestAuthority struct {
	nodev1.UnimplementedAuthorityServiceServer
	nodev1.UnimplementedTimeServiceServer
	nodev1.UnimplementedCreditServiceServer
	server *guestServer
}

func (g *guestAuthority) Fetch(ctx context.Context, request *nodev1.CreditDeliveryRequest) (*nodev1.CreditUpdateBatch, error) {
	s := g.server
	p, err := identity.FromContext(ctx, s.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return nil, err
	}
	if authority.ValidateCreditRequest(request) != nil || p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, identity.ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journals == nil || s.journals.AgentStateJournalId != request.AgentStateJournalId {
		return nil, identity.ErrIdentity
	}
	for _, runtime := range s.journals.Runtimes {
		if runtime.RuntimeId == request.RuntimeId && runtime.StateJournalId == request.StateJournalId && runtime.CreditJournalId == request.CreditJournalId {
			s.stats.CreditPolls++
			return &nodev1.CreditUpdateBatch{NodeId: request.NodeId, InstallationId: request.InstallationId, AgentStateJournalId: request.AgentStateJournalId, RuntimeId: request.RuntimeId, StateJournalId: request.StateJournalId, CreditJournalId: request.CreditJournalId}, nil
		}
	}
	return nil, identity.ErrIdentity
}

func (g *guestAuthority) RegisterJournals(ctx context.Context, request *nodev1.JournalRegistration) (*nodev1.JournalRegistrationReceipt, error) {
	s := g.server
	p, err := identity.FromContext(ctx, s.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return nil, err
	}
	request, digest, err := authority.NormalizeRegistration(request)
	if err != nil || p.Require(request.NodeId, request.InstallationId) != nil {
		return nil, identity.ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journals != nil && !proto.Equal(s.journals, request) {
		return nil, errors.New("guest journal identity replaced")
	}
	s.journals = request
	s.stats.JournalRuntimes = len(request.Runtimes)
	return &nodev1.JournalRegistrationReceipt{AgentStateJournalId: request.AgentStateJournalId, RegistrationSha256: digest}, nil
}

func (g *guestAuthority) Report(ctx context.Context, report *nodev1.AuthorityReport) (*nodev1.AuthorityReceipt, error) {
	s := g.server
	p, err := identity.FromContext(ctx, s.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return nil, err
	}
	if authority.ValidateReport(report, true) != nil || p.Require(report.NodeId, report.InstallationId) != nil || len(report.Grants) != 0 {
		return nil, errors.New("guest fixture accepts idle authority only")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journals == nil || s.journals.AgentStateJournalId != report.AgentStateJournalId {
		return nil, identity.ErrIdentity
	}
	matched := false
	for _, runtime := range s.journals.Runtimes {
		if runtime.RuntimeId == report.RuntimeId && runtime.StateJournalId == report.StateJournalId && runtime.CreditJournalId == report.CreditJournalId {
			matched = true
		}
	}
	if !matched {
		return nil, identity.ErrIdentity
	}
	previous := s.reports[report.RuntimeId]
	if previous != nil && report.Sequence == previous.Sequence && !proto.Equal(previous, report) {
		return nil, errors.New("guest authority replay conflict")
	}
	if previous != nil && report.Sequence < previous.Sequence || previous != nil && report.Sequence > previous.Sequence+1 || previous == nil && report.Sequence != 1 {
		return nil, errors.New("guest authority sequence rejected")
	}
	if s.reports == nil {
		s.reports = make(map[string]*nodev1.AuthorityReport)
	}
	if previous == nil || report.Sequence > previous.Sequence {
		s.stats.AuthorityReports++
	}
	s.reports[report.RuntimeId] = report
	return &nodev1.AuthorityReceipt{RuntimeId: report.RuntimeId, StateJournalId: report.StateJournalId, CreditJournalId: report.CreditJournalId, Sequence: report.Sequence}, nil
}

func (g *guestAuthority) Synchronize(ctx context.Context, request *nodev1.TimeRequest) (*nodev1.TimeResponse, error) {
	p, err := identity.FromContext(ctx, g.server.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return nil, err
	}
	if request == nil || p.Require(request.NodeId, request.InstallationId) != nil || !authority.ValidID(request.Nonce) {
		return nil, identity.ErrIdentity
	}
	g.server.mu.Lock()
	g.server.stats.TimeSynchronizations++
	g.server.mu.Unlock()
	return &nodev1.TimeResponse{NodeId: request.NodeId, InstallationId: request.InstallationId, Nonce: request.Nonce, ServerUnixMilli: time.Now().UnixMilli()}, nil
}
