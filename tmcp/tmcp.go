package tmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport"
	"github.com/nexssp/transport/thttp"
)

const ProtocolVersion = "2024-11-05"

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Resource struct {
	URI         string                                    `json:"uri"`
	Name        string                                    `json:"name"`
	Description string                                    `json:"description,omitempty"`
	MimeType    string                                    `json:"mimeType,omitempty"`
	ReadFn      func(ctx context.Context) (string, error) `json:"-"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptTemplate struct {
	Name        string                                                            `json:"name"`
	Description string                                                            `json:"description,omitempty"`
	Arguments   []PromptArgument                                                  `json:"arguments,omitempty"`
	BuildPrompt func(ctx context.Context, args map[string]string) (string, error) `json:"-"`
}

type sseSession struct {
	ch chan JSONRPCResponse
}

type Transport struct {
	serverName string
	version    string
	actions    map[string]action.AnyAction
	resources  map[string]Resource
	templates  []ResourceTemplate
	prompts    map[string]PromptTemplate
	sessions   map[string]*sseSession // new
	mu         sync.RWMutex
}

var _ transport.Transport = (*Transport)(nil)

func New(serverName, version string) *Transport {
	if serverName == "" {
		serverName = "nexss-mcp-server"
	}
	if version == "" {
		version = "1.0.0"
	}
	return &Transport{
		serverName: serverName,
		version:    version,
		actions:    make(map[string]action.AnyAction),
		resources:  make(map[string]Resource),
		prompts:    make(map[string]PromptTemplate),
		sessions:   make(map[string]*sseSession),
	}
}

// CanHandle returns false because MCP registers tools via reflection on Mount.
func (t *Transport) CanHandle(_ action.Binding) bool { return false }
func (t *Transport) String() string                  { return "mcp(" + t.serverName + ")" }

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

func (t *Transport) Mount(actions []action.AnyAction) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, a := range actions {
		if meta := a.Describe(); meta != nil && meta.Name != "" {
			t.actions[meta.Name] = a

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
	return action.New("transport.mcp."+t.serverName, func(ctx context.Context, _ any) (any, error) {
		return t.Do(ctx, nil)
	}).
		Tag("infra", "transport", "mcp").
		Description("MCP Server: " + t.serverName)
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	return nil, t.ServeStdio(ctx)
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

		if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			var req JSONRPCRequest
			if unmarshalErr := json.Unmarshal(line, &req); unmarshalErr != nil {
				_ = encoder.Encode(JSONRPCResponse{
					JSONRPC: "2.0",
					Error:   RPCError{Code: -32700, Message: "Parse error"},
				})
			} else {
				resp := t.dispatchRPC(ctx, req)
				if req.ID != nil {
					_ = encoder.Encode(resp)
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

func (t *Transport) ServeStdio(ctx context.Context) error {
	return t.Serve(ctx, os.Stdin, os.Stdout)
}

// HTTPRoutes returns raw HTTP actions for mounting MCP SSE endpoints.
func (t *Transport) HTTPRoutes() []action.AnyAction {
	sseAction := action.New[any, any]("mcp.sse", nil).
		Description("Model Context Protocol SSE Transport").
		Tag("infra", "mcp").
		Route(thttp.RawHandler("GET", "/mcp/sse", t.serveSSE)).
		Build()

	postAction := action.New[any, any]("mcp.message", nil).
		Description("Model Context Protocol Message Endpoint").
		Tag("infra", "mcp").
		Route(thttp.RawHandler("POST", "/mcp/message", t.serveHTTPMessage)).
		Build()

	return []action.AnyAction{sseAction, postAction}
}

func (t *Transport) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	sess := &sseSession{ch: make(chan JSONRPCResponse, 16)}

	t.mu.Lock()
	t.sessions[sessionID] = sess
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.sessions, sessionID)
		t.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fmt.Fprintf(w, "event: endpoint\ndata: /mcp/message?sessionId=%s\n\n", sessionID)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case resp, ok := <-sess.ch:
			if !ok {
				return
			}
			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (t *Transport) serveHTTPMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   RPCError{Code: -32700, Message: "Malformed JSON payload"},
		})
		return
	}

	resp := t.dispatchRPC(r.Context(), req)

	if sessionID == "" {
		// No SSE session in play — fall back to a synchronous JSON response.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	t.mu.RLock()
	sess, ok := t.sessions[sessionID]
	t.mu.RUnlock()

	if !ok {
		http.Error(w, `{"error":"unknown or expired session"}`, http.StatusNotFound)
		return
	}

	if req.ID != nil {
		select {
		case sess.ch <- resp:
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			http.Error(w, `{"error":"session delivery timeout"}`, http.StatusGatewayTimeout)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (t *Transport) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mcp/sse", t.serveSSE)
	mux.HandleFunc("POST /mcp/message", t.serveHTTPMessage)
	return mux
}

func (t *Transport) dispatchRPC(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": ProtocolVersion,
				"serverInfo": map[string]string{
					"name":    t.serverName,
					"version": t.version,
				},
				"capabilities": map[string]any{
					"tools":     map[string]bool{"listChanged": false},
					"resources": map[string]bool{"subscribe": false, "listChanged": false},
					"prompts":   map[string]bool{"listChanged": false},
				},
			},
		}

	case "notifications/initialized", "ping":
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		t.mu.RLock()
		defer t.mu.RUnlock()

		tools := make([]map[string]any, 0, len(t.actions))
		for name, act := range t.actions {
			meta := act.Describe()
			var schema any = map[string]any{"type": "object", "properties": map[string]any{}}
			if typed, ok := act.(action.TypedPayload); ok {
				if req := typed.ReqPayload(); req != nil {
					schema = buildJSONSchema(reflect.TypeOf(req))
				}
			}

			tools = append(tools, map[string]any{
				"name":        name,
				"description": meta.Description,
				"inputSchema": schema,
			})
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "Invalid params"}}
		}

		t.mu.RLock()
		act, ok := t.actions[p.Name]
		t.mu.RUnlock()

		if !ok {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32601, Message: "Tool not found"}}
		}

		exec, ok := act.(action.Executable)
		if !ok {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32603, Message: "Action is not executable"}}
		}

		decodeFn := func(target any) error {
			if len(p.Arguments) > 0 && string(p.Arguments) != "null" && string(p.Arguments) != "{}" {
				return json.Unmarshal(p.Arguments, target)
			}
			return nil
		}

		res, err := exec.ExecuteDecoded(ctx, decodeFn)
		if err != nil {
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"isError": true,
					"content": []any{
						map[string]string{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
				},
			}
		}

		resBytes, _ := json.MarshalIndent(res, "", "  ")
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []any{
					map[string]string{"type": "text", "text": string(resBytes)},
				},
			},
		}

	case "resources/list":
		t.mu.RLock()
		defer t.mu.RUnlock()

		resList := make([]map[string]any, 0, len(t.resources))
		for _, r := range t.resources {
			resList = append(resList, map[string]any{
				"uri":         r.URI,
				"name":        r.Name,
				"description": r.Description,
				"mimeType":    r.MimeType,
			})
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": resList}}

	case "resources/templates/list":
		t.mu.RLock()
		defer t.mu.RUnlock()
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resourceTemplates": t.templates}}

	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "Invalid params"}}
		}

		t.mu.RLock()
		resObj, ok := t.resources[p.URI]
		t.mu.RUnlock()

		if !ok || resObj.ReadFn == nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: fmt.Sprintf("Resource %q not found", p.URI)}}
		}

		content, err := resObj.ReadFn(ctx)
		if err != nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32603, Message: err.Error()}}
		}

		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"contents": []map[string]any{
					{
						"uri":      resObj.URI,
						"mimeType": resObj.MimeType,
						"text":     content,
					},
				},
			},
		}

	case "prompts/list":
		t.mu.RLock()
		defer t.mu.RUnlock()

		promptList := make([]map[string]any, 0, len(t.prompts))
		for _, pr := range t.prompts {
			promptList = append(promptList, map[string]any{
				"name":        pr.Name,
				"description": pr.Description,
				"arguments":   pr.Arguments,
			})
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": promptList}}

	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "Invalid params"}}
		}

		t.mu.RLock()
		promptObj, ok := t.prompts[p.Name]
		t.mu.RUnlock()

		if !ok || promptObj.BuildPrompt == nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: fmt.Sprintf("Prompt %q not found", p.Name)}}
		}

		rendered, err := promptObj.BuildPrompt(ctx, p.Arguments)
		if err != nil {
			return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32603, Message: err.Error()}}
		}

		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"description": promptObj.Description,
				"messages": []map[string]any{
					{
						"role":    "user",
						"content": map[string]string{"type": "text", "text": rendered},
					},
				},
			},
		}
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   RPCError{Code: -32601, Message: fmt.Sprintf("Method %q not supported", req.Method)},
	}
}

func buildJSONSchema(t reflect.Type) map[string]any {
	return buildJSONSchemaVisited(t, map[reflect.Type]bool{})
}

func buildJSONSchemaVisited(t reflect.Type, visiting map[reflect.Type]bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		// Guard against infinite recursion on self-referential/cyclic types.
		if visiting[t] {
			return map[string]any{"type": "object"}
		}
		visiting[t] = true
		defer delete(visiting, t)

		properties := make(map[string]any)
		var required []string

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			jsonTag := strings.Split(f.Tag.Get("json"), ",")[0]
			if jsonTag == "-" {
				continue
			}
			if jsonTag == "" {
				jsonTag = f.Name
			}

			valTag := f.Tag.Get("validate")
			if strings.Contains(valTag, "required") {
				required = append(required, jsonTag)
			}

			properties[jsonTag] = buildJSONSchemaVisited(f.Type, visiting)
		}

		out := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out

	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.String:
		return map[string]any{"type": "string"}

	default:
		return map[string]any{"type": "string"}
	}
}
