package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/diagnostics"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
)

func CheckRuntimes(ctx context.Context, endpoints []RuntimeEndpoint) error {
	if len(endpoints) == 0 || len(endpoints) > 32 {
		return configuration.ErrConfiguration
	}
	seen := make(map[string]bool)
	for _, endpoint := range endpoints {
		if endpoint.ID == "" || endpoint.Core == "" || seen[endpoint.ID] {
			return configuration.ErrConfiguration
		}
		seen[endpoint.ID] = true
		if err := checkRuntime(ctx, endpoint); err != nil {
			return fmt.Errorf("runtime %s: %w", endpoint.ID, err)
		}
	}
	return nil
}

func checkRuntime(ctx context.Context, endpoint RuntimeEndpoint) error {
	client, err := localrpc.Client(endpoint.Socket, endpoint.UID)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	condition, err := readRuntimeCondition(ctx, client, endpoint.ID)
	if err != nil {
		return err
	}
	if !condition.Ready {
		return fmt.Errorf("runtime is not ready: %s", condition.Condition)
	}
	return nil
}

func readRuntimeCondition(ctx context.Context, client *http.Client, id string) (*nodev1.RuntimeCondition, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://runtime/v1/status", nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return nil, err
	}
	if len(data) > 4096 {
		return nil, diagnostics.ErrConditions
	}
	condition := new(nodev1.RuntimeCondition)
	if proto.Unmarshal(data, condition) != nil || condition.ResourceId != id {
		return nil, diagnostics.ErrConditions
	}
	clean, err := diagnostics.Conditions([]*nodev1.RuntimeCondition{condition})
	if err != nil {
		return nil, err
	}
	return clean[0], nil
}

func (l *LocalRuntimes) Conditions(ctx context.Context) []*nodev1.RuntimeCondition {
	result := make([]*nodev1.RuntimeCondition, len(l.workers))
	var group sync.WaitGroup
	for i, worker := range l.workers {
		group.Go(func() {
			condition, err := readRuntimeCondition(ctx, worker.client, worker.endpoint.ID)
			if err != nil {
				code := "UNREACHABLE"
				if errors.Is(err, diagnostics.ErrConditions) {
					code = "STATUS_INVALID"
				}
				condition = &nodev1.RuntimeCondition{ResourceId: worker.endpoint.ID, Condition: code}
			}
			result[i] = condition
		})
	}
	group.Wait()
	return result
}
