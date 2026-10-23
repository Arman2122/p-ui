package nodeconfig

import "encoding/json"

func resolveXraySelectors(base map[string]json.RawMessage, input Input) error {
	if len(base["routing"]) == 0 {
		return nil
	}
	clients, tags := map[string]string{}, map[string]string{}
	bind := func(index map[string]string, key, value string) error {
		if key == "" {
			return nil
		}
		if before := index[key]; before != "" && before != value {
			return ErrConflict
		}
		index[key] = value
		return nil
	}
	for _, inbound := range input.Inbounds {
		if err := bind(tags, inbound.Instance.Tag, inbound.TargetTag); err != nil {
			return err
		}
		if err := bind(tags, inbound.TargetTag, inbound.TargetTag); err != nil {
			return err
		}
		for _, client := range inbound.Clients {
			if err := bind(clients, client.User.Email, client.ClientID); err != nil {
				return err
			}
			if err := bind(clients, client.ClientID, client.ClientID); err != nil {
				return err
			}
		}
	}
	var routing map[string]json.RawMessage
	if json.Unmarshal(base["routing"], &routing) != nil || routing == nil {
		return ErrInvalid
	}
	var rules []map[string]json.RawMessage
	if raw := routing["rules"]; len(raw) != 0 && json.Unmarshal(raw, &rules) != nil {
		return ErrInvalid
	}
	for _, rule := range rules {
		for field, index := range map[string]map[string]string{"user": clients, "inboundTag": tags} {
			raw, exists := rule[field]
			if !exists {
				continue
			}
			var selected []string
			if json.Unmarshal(raw, &selected) != nil || len(selected) == 0 {
				return ErrInvalid
			}
			for i, value := range selected {
				resolved, ok := index[value]
				if !ok || resolved == "" {
					return ErrDependency
				}
				selected[i] = resolved
			}
			encoded, err := json.Marshal(selected)
			if err != nil {
				return err
			}
			rule[field] = encoded
		}
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	routing["rules"] = encoded
	base["routing"], err = json.Marshal(routing)
	return err
}
