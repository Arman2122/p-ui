package core

import (
	"errors"
	"fmt"
)

var ErrSourceCredentialConflict = errors.New("supplied source credential is incompatible")

// SourceCredentialAdditions rejects legacy repair behavior without exposing
// secret values. Callers may use it only with a pure credential minter.
func SourceCredentialAdditions(have, minted map[string]string) (SourceCredentialResult, error) {
	result := SourceCredentialResult{Values: make(map[string]string, len(minted))}
	for name, value := range minted {
		if supplied := have[name]; supplied != "" {
			if supplied != value {
				return SourceCredentialResult{}, fmt.Errorf("%w: %s", ErrSourceCredentialConflict, name)
			}
			continue
		}
		result.Values[name] = value
	}
	return result, nil
}
