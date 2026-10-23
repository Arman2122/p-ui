package controlplane

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type (
	connectedNode struct {
		wake       chan struct{}
		cancel     context.CancelFunc
		generation uint64
	}
	Service struct {
		nodev1.UnimplementedControlServiceServer
		nodev1.UnimplementedAccountingServiceServer
		store             *Store
		plane             string
		mu                sync.Mutex
		sessions          map[string]*connectedNode
		activation        ActivationHandler
		creditDelivery    CreditDeliveryValidator
		authorityDelivery AuthorityDeliveryValidator
		activations       chan struct{}
	}
)

func NewService(store *Store, plane string, activation ...ActivationHandler) (*Service, error) {
	if store == nil || !validID(plane) || len(activation) > 1 {
		return nil, ErrInvalid
	}
	service := &Service{store: store, plane: plane, sessions: make(map[string]*connectedNode), activations: make(chan struct{}, 32)}
	if len(activation) == 1 {
		service.activation = activation[0]
	}
	return service, nil
}

func (s *Service) Publish(ctx context.Context, nodeID string, payload []byte) (uint64, error) {
	revision, err := s.store.Publish(ctx, nodeID, payload)
	if err != nil {
		return 0, err
	}
	s.wake(nodeID)
	return revision, nil
}

func (s *Service) PublishConfiguration(ctx context.Context, p identity.Principal, bundle configuration.Bundle) (uint64, error) {
	revision, err := s.store.PublishConfiguration(ctx, p, bundle)
	if err != nil {
		return 0, err
	}
	s.wake(p.NodeID)
	return revision, nil
}

func (s *Service) wake(nodeID string) {
	s.mu.Lock()
	if session := s.sessions[nodeID]; session != nil {
		select {
		case session.wake <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

type nodeEvent struct {
	message *nodev1.NodeMessage
	err     error
}

func (s *Service) Connect(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.MasterMessage]) error {
	p, err := identity.FromContext(stream.Context(), s.plane)
	if err != nil {
		return status.Error(codes.Unauthenticated, "node certificate required")
	}
	first, err := receiveHello(stream)
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || hello.ProtocolVersion != compatibility.Protocol || len(hello.Capabilities) > 128 ||
		p.Require(hello.NodeId, hello.InstallationId) != nil {
		return status.Error(codes.PermissionDenied, "node hello identity or version rejected")
	}
	generation, err := s.store.BeginSession(stream.Context(), p, hello)
	if err != nil {
		return status.Error(codes.PermissionDenied, "node session rejected")
	}
	ctx, cancel := context.WithCancel(stream.Context())
	current := &connectedNode{wake: make(chan struct{}, 1), cancel: cancel, generation: generation}
	s.mu.Lock()
	if previous := s.sessions[p.NodeID]; previous != nil {
		if previous.generation >= generation {
			s.mu.Unlock()
			cancel()
			return status.Error(codes.Aborted, "node session superseded")
		}
		previous.cancel()
	}
	s.sessions[p.NodeID] = current
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if s.sessions[p.NodeID] == current {
			delete(s.sessions, p.NodeID)
		}
		s.mu.Unlock()
	}()
	if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Authority{Authority: &nodev1.SessionAuthority{
		NodeId: p.NodeID, ControlPlaneId: p.ControlPlaneID, Generation: generation, ProtocolVersion: compatibility.Protocol,
	}}}); err != nil {
		return err
	}
	events := make(chan nodeEvent, 1)
	go func() {
		for {
			message, err := stream.Recv()
			select {
			case events <- nodeEvent{message: message, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var attempted uint64
	var pending bool
	retryTimer := time.NewTimer(time.Hour)
	retryTimer.Stop()
	defer retryTimer.Stop()
	var retry <-chan time.Time
	retryDelay := time.Second
	send := func(force bool) error {
		if pending {
			return nil
		}
		deployment, err := s.store.Delivery(ctx, p, generation)
		if err != nil {
			return err
		}
		if deployment == nil || deployment.Revision == attempted && !force {
			return nil
		}
		if deployment.Revision != attempted {
			retryDelay = time.Second
		}
		retryTimer.Stop()
		retry = nil
		if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Deployment{Deployment: deployment}}); err != nil {
			return err
		}
		attempted = deployment.Revision
		pending = true
		return nil
	}
	if err := send(false); err != nil {
		return status.Error(codes.Unavailable, "deployment delivery unavailable")
	}
	activationResults := make(chan *nodev1.ActivationResult, 4)
	activationSlots := make(chan struct{}, 4)
	var activationWG sync.WaitGroup
	defer func() { cancel(); activationWG.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case result := <-activationResults:
			if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Activation{Activation: result}}); err != nil {
				return err
			}
		case <-retry:
			retry = nil
			if err := send(true); err != nil {
				return status.Error(codes.Unavailable, "deployment retry unavailable")
			}
		case <-ticker.C:
			if err := s.store.CheckSession(ctx, p, generation, false); err != nil {
				return status.Error(codes.PermissionDenied, "node session revoked")
			}
			if _, err := identity.FromContext(stream.Context(), s.plane); err != nil {
				return status.Error(codes.Unauthenticated, "node certificate expired")
			}
			if err := send(false); err != nil {
				return status.Error(codes.Unavailable, "deployment delivery unavailable")
			}
		case <-current.wake:
			if err := send(false); err != nil {
				return status.Error(codes.Unavailable, "deployment delivery unavailable")
			}
		case event := <-events:
			if errors.Is(event.err, io.EOF) {
				return nil
			}
			if event.err != nil {
				return event.err
			}
			if _, err := identity.FromContext(stream.Context(), s.plane); err != nil {
				return status.Error(codes.Unauthenticated, "node certificate expired")
			}
			switch message := event.message.Payload.(type) {
			case *nodev1.NodeMessage_Result:
				if message.Result == nil || message.Result.Revision != attempted || !pending {
					return status.Error(codes.InvalidArgument, "unexpected deployment result")
				}
				if err := s.store.Observe(ctx, p, generation, message.Result); err != nil {
					return status.Error(codes.FailedPrecondition, "deployment result rejected")
				}
				if message.Result.Stage != nodev1.ApplyStage_APPLY_STAGE_RECEIVED {
					pending = false
					if message.Result.Stage == nodev1.ApplyStage_APPLY_STAGE_REJECTED {
						retryTimer.Reset(retryDelay + time.Duration(rand.Int64N(int64(retryDelay/2))))
						retry = retryTimer.C
						retryDelay = min(30*time.Second, retryDelay*2)
					}
					if err := send(false); err != nil {
						return status.Error(codes.Unavailable, "deployment delivery unavailable")
					}
				}
			case *nodev1.NodeMessage_Heartbeat:
				if message.Heartbeat == nil {
					return status.Error(codes.InvalidArgument, "empty heartbeat")
				}
				if err := s.store.Heartbeat(ctx, p, generation, message.Heartbeat); err != nil {
					return status.Error(codes.PermissionDenied, "node session revoked")
				}
			case *nodev1.NodeMessage_Activation:
				select {
				case activationSlots <- struct{}{}:
					activationWG.Go(func() {
						defer func() { <-activationSlots }()
						result := s.activate(ctx, p, generation, message.Activation)
						select {
						case activationResults <- result:
						case <-ctx.Done():
						}
					})
				default:
					result := &nodev1.ActivationResult{RequestId: message.Activation.GetRequestId(), ClientId: message.Activation.GetClientId(), ErrorCode: "ACTIVATION_BUSY"}
					if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Activation{Activation: result}}); err != nil {
						return err
					}
				}
			default:
				return status.Error(codes.InvalidArgument, "unsupported control message")
			}
		}
	}
}

func (s *Service) Report(stream grpc.BidiStreamingServer[nodev1.UsageBatch, nodev1.UsageReceipt]) error {
	if _, err := identity.FromContext(stream.Context(), s.plane); err != nil {
		return status.Error(codes.Unauthenticated, "node certificate required")
	}
	for {
		batch, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := identity.FromContext(stream.Context(), s.plane)
		if err != nil {
			return status.Error(codes.Unauthenticated, "node certificate required")
		}
		receipt, err := s.store.Ingest(stream.Context(), p, batch)
		if err != nil {
			return status.Error(codes.FailedPrecondition, "accounting batch rejected")
		}
		if err := stream.Send(receipt); err != nil {
			return err
		}
	}
}

func receiveHello(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.MasterMessage]) (*nodev1.NodeMessage, error) {
	result := make(chan nodeEvent, 1)
	go func() { message, err := stream.Recv(); result <- nodeEvent{message: message, err: err} }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case event := <-result:
		return event.message, event.err
	case <-stream.Context().Done():
		return nil, stream.Context().Err()
	case <-timer.C:
		return nil, status.Error(codes.DeadlineExceeded, "node hello deadline exceeded")
	}
}
