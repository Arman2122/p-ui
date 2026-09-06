//go:build linux

package banset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type kernelPlane struct{}

func hostPlane() Plane { return kernelPlane{} }

// Probe asks nft for a listing rather than checking for the binary: the binary
// answers on a host where the panel may not write, and the listing does not.
func (kernelPlane) Probe(ctx context.Context) error {
	nft, err := exec.LookPath("nft")
	if err != nil {
		return ErrNoNft
	}
	out, err := exec.CommandContext(ctx, nft, "list", "tables").CombinedOutput()
	if err != nil {
		return classify(err, string(out))
	}
	return nil
}

func (kernelPlane) Apply(ctx context.Context, script string) error {
	nft, err := exec.LookPath("nft")
	if err != nil {
		return ErrNoNft
	}
	cmd := exec.CommandContext(ctx, nft, "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return classify(err, string(out))
	}
	return nil
}

func classify(err error, out string) error {
	out = strings.TrimSpace(out)
	if errors.Is(err, os.ErrPermission) || strings.Contains(out, "Operation not permitted") {
		return fmt.Errorf("%w: %s", ErrPermission, out)
	}
	return fmt.Errorf("banset: nft: %w: %s", err, out)
}
