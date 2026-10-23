package nativeruntime

import (
	"context"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

func validateNativeAdmissions(config Configuration, descriptors []admission.Descriptor, grants map[string]governor.Grant) error {
	for _, inbound := range config.WireGuard {
		for _, peer := range inbound.Clients {
			if grants[peer.ClientID].ID == "" {
				return configuration.ErrConfiguration
			}
		}
	}
	for _, inbound := range config.AmneziaWG {
		for _, peer := range inbound.Clients {
			if grants[peer.ClientID].ID == "" {
				return configuration.ErrConfiguration
			}
		}
	}
	if len(descriptors) == 0 {
		return nil
	}
	subjects := map[admission.Subject]bool{}
	tags := map[string]bool{}
	add := func(tag string, clients []string) error {
		if tag == "" || tags[tag] {
			return configuration.ErrConfiguration
		}
		tags[tag] = true
		for _, client := range clients {
			subjects[admission.Subject{ClientID: client, InboundTag: tag}] = true
		}
		return nil
	}
	for _, inbound := range config.OpenVPN {
		clients := make([]string, 0, len(inbound.Clients))
		for _, client := range inbound.Clients {
			clients = append(clients, client.ClientID)
		}
		if err := add(inbound.InboundTag, clients); err != nil {
			return err
		}
	}
	for _, inbound := range config.OpenConnect {
		clients := make([]string, 0, len(inbound.Clients))
		for _, client := range inbound.Clients {
			clients = append(clients, client.ClientID)
		}
		if err := add(inbound.InboundTag, clients); err != nil {
			return err
		}
	}
	for _, descriptor := range descriptors {
		subject := admission.Subject{ClientID: descriptor.ClientID, InboundTag: descriptor.InboundTag}
		if !subjects[subject] {
			return configuration.ErrConfiguration
		}
		delete(subjects, subject)
	}
	if len(subjects) != 0 {
		return configuration.ErrConfiguration
	}
	return nil
}

func captureNativeAuthentication(ctx context.Context, runtime *configuration.Runtime) (*admission.Binding, map[admission.Subject]*admission.BoundProof, error) {
	if runtime == nil || len(runtime.Admissions) == 0 {
		return nil, nil, nil
	}
	binding := admission.BindingFromContext(ctx)
	proofs := make(map[admission.Subject]*admission.BoundProof, len(runtime.Admissions))
	for _, descriptor := range runtime.Admissions {
		proof, found := binding.Capture(descriptor.ClientID, descriptor.InboundTag)
		if !found || proof.Descriptor() != descriptor {
			return nil, nil, admission.ErrUnavailable
		}
		proofs[admission.Subject{ClientID: descriptor.ClientID, InboundTag: descriptor.InboundTag}] = proof
	}
	return binding, proofs, nil
}
