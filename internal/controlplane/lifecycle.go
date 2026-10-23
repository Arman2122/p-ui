package controlplane

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"
)

type Manager struct {
	Activation        ActivationHandler
	CreditDelivery    CreditDeliveryValidator
	AuthorityDelivery AuthorityDeliveryValidator
	RefillScheduler   func(context.Context, *gorm.DB) error
	mu                sync.RWMutex
	host              *Host
	err               error
}

func (m *Manager) Current() (*Host, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.host == nil && m.err == nil {
		return nil, errors.New("node control plane is not configured")
	}
	return m.host, m.err
}

func (m *Manager) Run(ctx context.Context, db func() *gorm.DB, path string, report func(error)) {
	recoveryNeeded := false
	for ctx.Err() == nil {
		config, err := ReadHostConfig(path)
		if errors.Is(err, os.ErrNotExist) {
			m.mu.Lock()
			m.host, m.err = nil, nil
			m.mu.Unlock()
		} else {
			var host *Host
			pool := db()
			var control, bootstrap net.Listener
			if err == nil {
				var store *Store
				store, err = NewStore(pool)
				if err == nil && recoveryNeeded {
					err = store.HoldForRecovery(ctx)
					if err == nil {
						recoveryNeeded = false
					}
				}
				if err == nil {
					host, err = NewHost(ctx, store, config, m.Activation)
				}
				if err == nil {
					host.service.creditDelivery = m.CreditDelivery
					host.service.authorityDelivery = m.AuthorityDelivery
					control, bootstrap, err = host.listen(ctx)
				}
			}
			m.mu.Lock()
			m.host, m.err = nil, err
			if err == nil {
				m.host = host
			}
			m.mu.Unlock()
			if err == nil {
				err = m.serve(ctx, host, pool, db, control, bootstrap)
				if errors.Is(err, ErrRecovery) {
					recoveryNeeded = true
				}
			}
			m.mu.Lock()
			m.host, m.err = nil, err
			m.mu.Unlock()
			if err != nil && ctx.Err() == nil && report != nil {
				report(err)
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) serve(ctx context.Context, host *Host, pool *gorm.DB, current func() *gorm.DB, control, bootstrap net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer control.Close()
	defer bootstrap.Close()
	done := make(chan error, 1)
	go func() { done <- host.Serve(ctx, control, bootstrap) }()
	var refills chan error
	if m.RefillScheduler != nil {
		refills = make(chan error, 1)
		go func() { refills <- m.RefillScheduler(ctx, pool) }()
	}
	defer func() {
		cancel()
		if refills != nil {
			<-refills
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case err := <-refills:
			refills = nil
			cancel()
			<-done
			return err
		case <-ctx.Done():
			cancel()
			return <-done
		case <-ticker.C:
			if current() != pool {
				cancel()
				<-done
				return ErrRecovery
			}
		}
	}
}
