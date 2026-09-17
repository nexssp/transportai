package ta2a

import "context"

func (t *Transport) Card(ctx context.Context) (AgentCard, error) {
	if t.cardProvider != nil {
		return t.cardProvider.AgentCard(ctx)
	}

	return AgentCard{
		Name:        "nexss-a2a-agent",
		Version:     "1.0.0",
		Description: "A2A Agent supporting task lifecycle, HITL, and multi-part data",
		Capabilities: map[string]bool{
			"streaming":              true,
			"pushNotifications":      true,
			"stateTransitionHistory": true,
			"humanInTheLoop":         true,
		},
		DefaultInputModes:  []string{string(PartText), "data"},
		DefaultOutputModes: []string{string(PartText), "data"},
		Roles:              t.roleHelp(),
	}, nil
}

func (t *Transport) roleHelp() map[string]RoleHelp {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.bindings) == 0 {
		return nil
	}

	roles := make(map[string]RoleHelp, len(t.bindings))

	for role := range t.bindings {
		binding := t.bindings[role]

		roles[role] = RoleHelp{
			Description: binding.Description,
			Examples:    append([]string(nil), binding.Examples...),
		}
	}

	return roles
}
