package diagnostics

import (
	"errors"
	"regexp"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

var ErrConditions = errors.New("invalid runtime observations")

var resourceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// Conditions retain bounded status codes; daemon error text can contain credentials.
func Conditions(input []*nodev1.RuntimeCondition) ([]*nodev1.RuntimeCondition, error) {
	if len(input) > 32 {
		return nil, ErrConditions
	}
	result := make([]*nodev1.RuntimeCondition, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, condition := range input {
		if condition == nil || !resourceID.MatchString(condition.ResourceId) || seen[condition.ResourceId] {
			return nil, ErrConditions
		}
		ready := false
		switch condition.Condition {
		case "UNCONFIGURED", "CONFIGURATION_APPLIED":
			ready = true
		case "CONFIGURATION_PENDING", "CONFIGURATION_FAILED", "RESTORE_FAILED", "UNREACHABLE", "STATUS_INVALID":
		default:
			return nil, ErrConditions
		}
		if ready != condition.Ready {
			return nil, ErrConditions
		}
		seen[condition.ResourceId] = true
		result = append(result, &nodev1.RuntimeCondition{ResourceId: condition.ResourceId, Condition: condition.Condition, Ready: ready})
	}
	return result, nil
}
