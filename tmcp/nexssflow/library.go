// Package nexssflow adapts the MCP transport to Nexss Flow bundles and .nflow modifiers.
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

	"github.com/nexssp/transportai/tmcp"
)

const ID = "tmcp"

func init() {
	compiler.Register(ID, Bundle)
}

// Config controls the MCP server created by a Flow bundle. If Addr is empty,
// tmcp.listen serves stdio; otherwise it starts an HTTP listener on Addr.
type Config struct {
	Name    string `nflow:"name"    default:"nexssflow-mcp"`
	Version string `nflow:"version" default:"1.0.0"`
	Addr    string `nflow:"addr"`
}

// Bundle builds the MCP Flow extension from @require options.
func Bundle(opts map[string]string) compiler.Bundle {
	cfg, err := compiler.Decode[Config](opts)
	if err != nil {
		panic("transportai MCP Flow bundle: " + err.Error())
	}
	bundle, err := NewBundle(cfg)
	if err != nil {
		panic("transportai MCP Flow bundle: " + err.Error())
	}
	return bundle
}

// NewBundle builds an MCP Flow extension for an embedded Flow host.
func NewBundle(cfg Config) (compiler.Bundle, error) {
	if cfg.Name == "" {
		cfg.Name = "nexssflow-mcp"
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	transport := tmcp.New(cfg.Name, cfg.Version)
	return compiler.Bundle{
		ID:        ID,
		Libraries: []action.Library{library(transport, cfg.Addr)},
		Modifiers: Modifiers(),
	}, nil
}

// Library provides tmcp.listen, which mounts MCP-bound Flow pipelines and serves stdio.
func Library(transport *tmcp.Transport) action.Library {
	return library(transport, "")
}

func library(transport *tmcp.Transport, addr string) action.Library {
	if transport == nil {
		transport = tmcp.New("nexssflow-mcp", "1.0.0")
	}
	return action.Library{
		Name: "transport.tmcp",
		Actions: []action.AnyAction{
			listenAction(transport, addr),
		},
	}
}

// Modifiers returns the MCP tool modifier understood by .nflow.
func Modifiers() []compiler.Modifier {
	return []compiler.Modifier{{
		Name:      "mcp",
		ValueKind: compiler.ModifierKindString,
		Example:   `:mcp="calculator.compute"`,
		Apply: func(builder *action.Builder[any, any], raw string) error {
			name := strings.TrimSpace(raw)
			if name == "" {
				return errors.New("mcp tool name cannot be empty")
			}
			builder.Route(tmcp.Tool(name))
			return nil
		},
	}}
}

func listenAction(transport *tmcp.Transport, addr string) action.AnyAction {
	return action.New("tmcp.listen", func(ctx context.Context, input any) (any, error) {
		resolver := contracts.ActionResolverFromContext(ctx)
		if resolver == nil {
			return nil, errors.New("tmcp.listen: action resolver is nil")
		}
		actionsToMount, err := resolveActions(resolver, requestedEndpoints(input))
		if err != nil {
			return nil, err
		}
		if len(actionsToMount) == 0 {
			return nil, errors.New("tmcp.listen: no MCP-bound Flow pipelines found")
		}
		transport.Mount(actionsToMount)
		if addr != "" {
			if err := transport.ListenAndServe(ctx, addr); err != nil && ctx.Err() == nil {
				return nil, fmt.Errorf("tmcp.listen: serve HTTP: %w", err)
			}
			return nil, nil
		}
		if err := transport.ServeStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("tmcp.listen: serve stdio: %w", err)
		}
		return nil, nil
	}).Tag("transport", "tmcp").Build()
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
				return nil, xerr.NotFound("tmcp.listen: endpoint not found: " + name)
			}
			resolved = append(resolved, act)
		}
		return resolved, nil
	}

	lister, ok := resolver.(interface{ Actions() []action.AnyAction })
	if !ok {
		return nil, errors.New("tmcp.listen: Flow resolver cannot list actions; specify endpoints explicitly")
	}
	var actionsToMount []action.AnyAction
	for _, act := range lister.Actions() {
		if act == nil {
			continue
		}
		for _, binding := range act.GetBindings() {
			if _, ok := binding.(tmcp.ToolBinding); ok {
				actionsToMount = append(actionsToMount, act)
				break
			}
		}
	}
	return actionsToMount, nil
}
