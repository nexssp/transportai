// Package nexssflow adapts the A2A transport to Nexss Flow bundles and .nflow modifiers.
package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transportai/ta2a"
)

const ID = "ta2a"

func init() {
	compiler.Register(ID, Bundle)
}

// Config controls the A2A server created by a Flow bundle.
type Config struct {
	Addr        string `nflow:"addr"        default:":8090"`
	Name        string `nflow:"name"        default:"nexssflow-agent"`
	Version     string `nflow:"version"     default:"1.0.0"`
	Description string `nflow:"description" default:"A2A agent powered by Nexss Flow"`
}

// Bundle builds the A2A Flow extension from @require options.
func Bundle(opts map[string]string) compiler.Bundle {
	cfg, err := compiler.Decode[Config](opts)
	if err != nil {
		panic("transportai A2A Flow bundle: " + err.Error())
	}
	bundle, err := NewBundle(cfg)
	if err != nil {
		panic("transportai A2A Flow bundle: " + err.Error())
	}
	return bundle
}

// NewBundle builds an A2A Flow extension for an embedded Flow host.
func NewBundle(cfg Config) (compiler.Bundle, error) {
	if cfg.Addr == "" {
		cfg.Addr = ":8090"
	}
	if cfg.Name == "" {
		cfg.Name = "nexssflow-agent"
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	if cfg.Description == "" {
		cfg.Description = "A2A agent powered by Nexss Flow"
	}
	transport := ta2a.New(cfg.Addr, nil, ta2a.WithAgentCard(ta2a.AgentCard{
		Name:        cfg.Name,
		Version:     cfg.Version,
		Description: cfg.Description,
		Capabilities: map[string]bool{
			"streaming":              true,
			"humanInTheLoop":         true,
			"stateTransitionHistory": true,
		},
	}))
	return compiler.Bundle{
		ID:        ID,
		Libraries: []action.Library{Library(transport)},
		Modifiers: Modifiers(),
	}, nil
}

// Library provides ta2a.listen, which mounts A2A-bound Flow pipelines and serves HTTP.
func Library(transport *ta2a.Transport) action.Library {
	if transport == nil {
		transport = ta2a.New(":8090", nil)
	}
	return action.Library{
		Name: "transport.ta2a",
		Actions: []action.AnyAction{
			listenAction(transport),
		},
	}
}

// Modifiers returns the A2A role and approval modifiers understood by .nflow.
func Modifiers() []compiler.Modifier {
	return []compiler.Modifier{
		{
			Name:      "a2a",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a="billing.support"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				role := strings.TrimSpace(raw)
				if role == "" {
					return errors.New("a2a role name cannot be empty")
				}
				builder.Route(ta2a.Role(role))
				return nil
			},
		},
		{
			Name:      "a2a_prompt",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_prompt="Manager approval required"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				prompt := strings.TrimSpace(raw)
				return updateExistingOrNewBinding(builder, func(b *ta2a.AgentBinding) {
					if b.HITL == nil {
						b.HITL = &ta2a.HITLConfig{Options: []string{ta2a.HITLApprove, ta2a.HITLReject}}
					}
					b.HITL.Prompt = prompt
				})
			},
		},
		{
			Name:      "a2a_triggers",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_triggers="refund,payout"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				triggers := splitTriggers(raw)
				return updateExistingOrNewBinding(builder, func(b *ta2a.AgentBinding) {
					if b.HITL == nil {
						b.HITL = &ta2a.HITLConfig{Options: []string{ta2a.HITLApprove, ta2a.HITLReject}}
					}
					b.HITL.TriggerWords = triggers
				})
			},
		},
		{
			Name:      "a2a_artifact",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_artifact="receipt_{{.ticket_id}}.json"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				name := strings.TrimSpace(raw)
				return updateExistingOrNewBinding(builder, func(b *ta2a.AgentBinding) {
					b.ArtifactName = name
				})
			},
		},
		{
			Name:      "a2a_mime",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_mime="application/json"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				mime := strings.TrimSpace(raw)
				return updateExistingOrNewBinding(builder, func(b *ta2a.AgentBinding) {
					b.ArtifactMime = mime
				})
			},
		},
		{
			Name:      "a2a_summary",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_summary="Processed ticket {{.ticket_id}}"`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				tmpl := strings.TrimSpace(raw)
				return updateExistingOrNewBinding(builder, func(b *ta2a.AgentBinding) {
					b.SummaryTemplate = tmpl
				})
			},
		},
		// Retain backwards compatibility for legacy string-pipe syntax
		{
			Name:      "a2a_approval",
			ValueKind: compiler.ModifierKindString,
			Example:   `:a2a_approval="billing.refund|refund|A manager must approve this request."`,
			Apply: func(builder *action.Builder[any, any], raw string) error {
				parts := strings.SplitN(raw, "|", 3)
				if len(parts) != 3 {
					return errors.New("expected role|trigger[,trigger...]|prompt")
				}
				role := strings.TrimSpace(parts[0])
				triggers := splitTriggers(parts[1])
				prompt := strings.TrimSpace(parts[2])
				if role == "" || len(triggers) == 0 || prompt == "" {
					return errors.New("role, at least one trigger, and prompt are required")
				}
				builder.Route(ta2a.Role(role).WithHumanInTheLoop(ta2a.HITLConfig{
					TriggerWords: triggers,
					Prompt:       prompt,
				}))
				return nil
			},
		},
	}
}

func updateExistingOrNewBinding(builder *action.Builder[any, any], fn func(*ta2a.AgentBinding)) error {
	meta := builder.Describe()
	var current ta2a.AgentBinding
	var found bool

	// Safely inspect current bindings by building a temporary snapshot
	for _, b := range builder.Build().GetBindings() {
		if ab, ok := b.(ta2a.AgentBinding); ok {
			current = ab
			found = true
			break
		}
	}

	if !found && meta != nil && meta.Name != "" {
		current = ta2a.Role(meta.Name)
	}
	fn(&current)
	builder.Route(current)
	return nil
}

func splitTriggers(raw string) []string {
	var triggers []string
	for trigger := range strings.SplitSeq(raw, ",") {
		if trigger = strings.TrimSpace(trigger); trigger != "" {
			triggers = append(triggers, trigger)
		}
	}
	return triggers
}

func listenAction(transport *ta2a.Transport) action.AnyAction {
	return action.New("ta2a.listen", func(ctx context.Context, input any) (any, error) {
		resolver := contracts.ActionResolverFromContext(ctx)
		if resolver == nil {
			return nil, errors.New("ta2a.listen: action resolver is nil")
		}
		actionsToMount, err := resolveActions(resolver, requestedEndpoints(input))
		if err != nil {
			return nil, err
		}
		if len(actionsToMount) == 0 {
			return nil, errors.New("ta2a.listen: no A2A-bound Flow pipelines found")
		}
		transport.Mount(actionsToMount)
		_, err = transport.Do(ctx, nil)
		if errors.Is(err, context.Canceled) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("ta2a.listen: serve HTTP: %w", err)
		}
		return nil, nil
	}).Tag("transport", "ta2a").Build()
}

func requestedEndpoints(input any) []string {
	values, ok := input.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := values["endpoints"]
	if !ok {
		return nil
	}
	var endpoints []string
	switch list := raw.(type) {
	case []string:
		endpoints = append(endpoints, list...)
	case []any:
		for _, value := range list {
			if endpoint, ok := value.(string); ok {
				endpoints = append(endpoints, endpoint)
			}
		}
	}
	return endpoints
}

func resolveActions(resolver contracts.ActionResolver, endpoints []string) ([]action.AnyAction, error) {
	if len(endpoints) > 0 {
		resolved := make([]action.AnyAction, 0, len(endpoints))
		for _, name := range endpoints {
			act, ok := resolver.Action(name)
			if !ok {
				return nil, xerr.NotFound("ta2a.listen: endpoint not found: " + name)
			}
			resolved = append(resolved, act)
		}
		return resolved, nil
	}

	lister, ok := resolver.(interface{ Actions() []action.AnyAction })
	if !ok {
		return nil, errors.New("ta2a.listen: Flow resolver cannot list actions; specify endpoints explicitly")
	}
	var actionsToMount []action.AnyAction
	for _, act := range lister.Actions() {
		if act == nil {
			continue
		}
		for _, binding := range act.GetBindings() {
			if _, ok := binding.(ta2a.AgentBinding); ok {
				actionsToMount = append(actionsToMount, act)
				break
			}
		}
	}
	return actionsToMount, nil
}
