//go:build !linux

package nativeruntime

import (
	"context"
	"errors"
	"io"

	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

func Open(context.Context, string, string, *state.Store, credentials.Config) (worker.Backend, io.Closer, error) {
	return nil, nil, errors.New("native runtime requires Linux")
}

func OpenVPNLearn([]string) error { return errors.New("native runtime requires Linux") }

func OcservHook([]string) error { return errors.New("native runtime requires Linux") }
