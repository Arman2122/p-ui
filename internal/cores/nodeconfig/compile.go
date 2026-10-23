package nodeconfig

import (
	"slices"

	"github.com/Arman2122/p-ui/internal/cores"
)

// Compile emits node definitions and explicit dependencies, not forwarding authority.
// Publication must resolve bridge and file-materialization claims separately.
func Compile(input Input) (Result, error) {
	if _, err := newRenderer(input); err != nil {
		return Result{}, err
	}
	groups := map[string][]Inbound{}
	for _, inbound := range input.Inbounds {
		family, known := cores.NodeRuntimeFor(inbound.Instance.Kind)
		if !known {
			return Result{}, issue(inbound, "", "protocol", ErrUnsupported)
		}
		groups[family] = append(groups[family], inbound)
	}
	seen := map[string]bool{}
	for _, base := range input.Bases {
		if !slices.Contains([]string{"xray", "singbox", "mtproto", "native", "ssh"}, base.Core) || base.RuntimeID != base.Core || seen[base.RuntimeID] {
			return Result{}, ErrInvalid
		}
		seen[base.RuntimeID] = true
		if _, exists := groups[base.Core]; !exists {
			groups[base.Core] = nil
		}
	}
	var result Result
	for _, family := range []string{"xray", "singbox", "mtproto", "native", "ssh"} {
		inbounds, needed := groups[family]
		if !needed {
			continue
		}
		scoped := input
		scoped.Inbounds = inbounds
		var compiled Result
		var err error
		switch family {
		case "xray":
			compiled, err = CompileXray(scoped)
		case "singbox":
			compiled, err = CompileSingBox(scoped)
		case "mtproto":
			compiled, err = CompileMTProto(scoped)
		case "native":
			compiled, err = CompileNative(scoped)
		case "ssh":
			compiled, err = CompileSSH(scoped)
		}
		if err != nil {
			return Result{}, err
		}
		result.Runtimes = append(result.Runtimes, compiled.Runtimes...)
		result.Listeners = append(result.Listeners, compiled.Listeners...)
		result.Assets = append(result.Assets, compiled.Assets...)
		result.Bridges = append(result.Bridges, compiled.Bridges...)
		result.Omissions = append(result.Omissions, compiled.Omissions...)
	}
	return finishResult(result)
}
