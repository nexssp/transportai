package tmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/transport"
)

type Transport struct {
	serverInfo  ServerInfo
	logLevel    string
	actions     map[string]action.AnyAction
	resources   map[string]Resource
	templates   []ResourceTemplate
	prompts     map[string]PromptTemplate
	completions CompletionResolver
	routes      map[string]rpcHandler
	sessions    map[string]chan Response
	writerMu    sync.Mutex
	mu          sync.RWMutex
}

var _ transport.Transport = (*Transport)(nil)

func New(serverName, version string) *Transport {
	if serverName == "" {
		serverName = "nexss-mcp-server"
	}
	if version == "" {
		version = "1.0.0"
	}

	t := &Transport{
		serverInfo: ServerInfo{Name: serverName, Version: version},
		logLevel:   "info",
		actions:    make(map[string]action.AnyAction),
		resources:  make(map[string]Resource),
		templates:  make([]ResourceTemplate, 0),
		prompts:    make(map[string]PromptTemplate),
		sessions:   make(map[string]chan Response),
	}

	t.registerRoutes()
	return t
}

func (t *Transport) String() string {
	return "mcp(" + t.serverInfo.Name + ")"
}

func (t *Transport) CanHandle(_ action.Binding) bool { return false }

func (t *Transport) RegisterResource(r Resource) *Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources[r.URI] = r
	return t
}

func (t *Transport) RegisterTemplate(tmpl ResourceTemplate) *Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.templates = append(t.templates, tmpl)
	return t
}

func (t *Transport) RegisterPrompt(p PromptTemplate) *Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prompts[p.Name] = p
	return t
}

func (t *Transport) SetCompletionResolver(fn CompletionResolver) *Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completions = fn
	return t
}

type mcpStartKey struct{}

func (t *Transport) Mount(actions []action.AnyAction) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, a := range actions {
		if meta := a.Describe(); meta != nil && meta.Name != "" {
			t.actions[meta.Name] = a

			// Kernel-native observability for every MCP tool.
			a.AddAnyHook(action.AnyHook{
				Before: func(ctx context.Context, req any, meta *action.Meta) (context.Context, error) {
					return context.WithValue(ctx, mcpStartKey{}, time.Now()), nil
				},
				After: func(ctx context.Context, req any, res any, err error, meta *action.Meta) {
					start, _ := ctx.Value(mcpStartKey{}).(time.Time)
					slog.InfoContext(ctx, "mcp_tool_call",
						"tool", meta.Name,
						"duration_ms", time.Since(start).Milliseconds(),
						"error", err,
					)
				},
			})

			docURI := fmt.Sprintf("action://docs/%s", meta.Name)
			t.resources[docURI] = Resource{
				URI:         docURI,
				Name:        fmt.Sprintf("Doc: %s", meta.Name),
				Description: meta.Description,
				MimeType:    "text/markdown",
				ReadFn: func(ctx context.Context) (string, error) {
					return fmt.Sprintf("# Action: %s\n\n%s\n\nTags: %v", meta.Name, meta.Description, meta.Tags), nil
				},
			}
		}
	}
}

func (t *Transport) AsAction() *action.Builder[any, any] {
	return action.New("transport.mcp."+t.serverInfo.Name, func(ctx context.Context, _ any) (any, error) {
		return t.Do(ctx, nil)
	}).
		Tag("infra", "transport", "mcp").
		Description("MCP Server: " + t.serverInfo.Name)
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	return nil, t.ServeStdio(ctx)
}

func (t *Transport) ServeStdio(ctx context.Context) error {
	return t.Serve(ctx, os.Stdin, os.Stdout)
}

func (t *Transport) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')

		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var req Request
			if unmarshalErr := json.Unmarshal(trimmed, &req); unmarshalErr != nil {
				t.writeSafe(encoder, errorResponse(nil, CodeParseError, "Parse error"))
			} else {
				resp := t.dispatch(ctx, req)
				if req.ID != nil {
					t.writeSafe(encoder, resp)
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (t *Transport) writeSafe(enc *json.Encoder, resp Response) {
	t.writerMu.Lock()
	defer t.writerMu.Unlock()
	_ = enc.Encode(resp)
}

func (t *Transport) dispatch(ctx context.Context, req Request) Response {
	ctx, scope, release := xctx.NewScope(ctx)
	defer release()

	if req.Method != "" {
		scope.Endpoint = "mcp." + req.Method
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		return errorResponse(req.ID, CodeInvalidRequest, "Invalid Request")
	}

	handler, ok := t.routes[req.Method]
	if !ok {
		return errorResponse(req.ID, CodeMethodNotFound, fmt.Sprintf("Method %q not supported", req.Method))
	}

	return handler(ctx, req)
}

func (t *Transport) registerRoutes() {
	t.routes = map[string]rpcHandler{
		"initialize":                t.handleInitialize,
		"notifications/initialized": t.handleNoop,
		"ping":                      t.handleNoop,
		"logging/setLevel":          t.handleSetLogLevel,
		"tools/list":                t.handleToolsList,
		"tools/call":                t.handleToolsCall,
		"resources/list":            t.handleResourcesList,
		"resources/templates/list":  t.handleResourceTemplatesList,
		"resources/read":            t.handleResourcesRead,
		"prompts/list":              t.handlePromptsList,
		"prompts/get":               t.handlePromptsGet,
		"completion/complete":       t.handleCompletionComplete,
	}
}
