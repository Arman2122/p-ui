//go:build !linux

package release

import "context"

func repositoryDirectory(_ string) error { return nil }

func lockRepository(_ context.Context, _ string) (func(), error) { return func() {}, nil }
