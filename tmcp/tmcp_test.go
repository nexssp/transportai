package tmcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transportai/tmcp"
)

type AddReq struct {
	A int `json:"a"`
	B int `json:"b"`
}

type AddRes struct {
	Sum int `json:"sum"`
}

type CustomContentRes struct {
	Content string `json:"content"`
	Summary string `json:"summary"`
}

func TestTMCP_FullToolAndResourceExecution(t *testing.T) {
	t.Parallel()

	addAct := action.New("math.add", func(ctx context.Context, req AddReq) (AddRes, error) {
		return AddRes{Sum: req.A + req.B}, nil
	}).
		Description("Calculates sum of two integers").
		Build()

	srv := tmcp.New("math-mcp", "1.0.0")
	srv.Mount([]action.AnyAction{addAct})

	// 1. Test tools/list
	reqList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	var outList bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqList), &outList)

	var respList tmcp.JSONRPCResponse
	_ = json.Unmarshal(outList.Bytes(), &respList)
	tools := respList.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// 2. Test tools/call
	reqCall := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"math.add","arguments":{"a":10,"b":32}}}` + "\n"
	var outCall bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqCall), &outCall)

	var respCall tmcp.JSONRPCResponse
	_ = json.Unmarshal(outCall.Bytes(), &respCall)

	resMap := respCall.Result.(map[string]any)
	contents := resMap["content"].([]any)
	text := contents[0].(map[string]any)["text"].(string)

	var sumRes AddRes
	_ = json.Unmarshal([]byte(text), &sumRes)
	if sumRes.Sum != 42 {
		t.Fatalf("expected sum 42, got %d", sumRes.Sum)
	}

	// 3. Test resources/read for automatically generated action docs
	reqDoc := `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"action://docs/math.add"}}` + "\n"
	var outDoc bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqDoc), &outDoc)

	var respDoc tmcp.JSONRPCResponse
	_ = json.Unmarshal(outDoc.Bytes(), &respDoc)
	resContents := respDoc.Result.(map[string]any)["contents"].([]any)
	docText := resContents[0].(map[string]any)["text"].(string)

	if !strings.Contains(docText, "# Action: math.add") {
		t.Fatalf("expected action doc in resource read, got: %s", docText)
	}
}

func TestTMCP_FormatToolResponseText_DirectContentAndMessage(t *testing.T) {
	t.Parallel()

	contentAct := action.New("pack.mock", func(ctx context.Context, _ any) (CustomContentRes, error) {
		return CustomContentRes{
			Content: "# Markdown Context\n\n```go\nfunc Main() {}\n```",
			Summary: "Summary info",
		}, nil
	}).Build()

	srv := tmcp.New("pack-mcp", "1.0.0")
	srv.Mount([]action.AnyAction{contentAct})

	reqCall := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"pack.mock","arguments":{}}}` + "\n"
	var outCall bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqCall), &outCall)

	var respCall tmcp.JSONRPCResponse
	_ = json.Unmarshal(outCall.Bytes(), &respCall)

	resMap := respCall.Result.(map[string]any)
	contents := resMap["content"].([]any)
	text := contents[0].(map[string]any)["text"].(string)

	// Must be the direct markdown string, not a double-escaped JSON string of the parent struct
	if !strings.HasPrefix(text, "# Markdown Context") {
		t.Fatalf("expected direct markdown content, got:\n%s", text)
	}
}

func TestTMCP_LoggingSetLevel(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("log-mcp", "1.0.0")

	req := `{"jsonrpc":"2.0","id":1,"method":"logging/setLevel","params":{"level":"debug"}}` + "\n"
	var out bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(req), &out)

	var resp tmcp.JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal setLevel response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("expected no error from logging/setLevel, got: %v", resp.Error)
	}
}

func TestTMCP_CompletionComplete(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("completion-mcp", "1.0.0")
	srv.SetCompletionResolver(func(ctx context.Context, refType, refName, argName, value string) ([]string, error) {
		if refType == "ref/prompt" && argName == "profile" {
			all := []string{"arch", "review", "compact", "test"}
			var matches []string
			for _, p := range all {
				if strings.HasPrefix(p, value) {
					matches = append(matches, p)
				}
			}
			return matches, nil
		}
		return nil, nil
	})

	req := `{"jsonrpc":"2.0","id":1,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"review-prompt"},"argument":{"name":"profile","value":"re"}}}` + "\n"
	var out bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(req), &out)

	var resp tmcp.JSONRPCResponse
	_ = json.Unmarshal(out.Bytes(), &resp)

	resMap := resp.Result.(map[string]any)
	completion := resMap["completion"].(map[string]any)
	values := completion["values"].([]any)

	if len(values) != 1 || values[0].(string) != "review" {
		t.Fatalf("expected ['review'], got: %v", values)
	}
}

func TestTMCP_PromptsListAndGet(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("prompt-mcp", "1.0.0")
	srv.RegisterPrompt(tmcp.PromptTemplate{
		Name:        "review-code",
		Description: "Reviews code architecture",
		Arguments: []tmcp.PromptArgument{
			{Name: "target", Description: "Target folder", Required: true},
		},
		BuildPrompt: func(ctx context.Context, args map[string]string) (string, error) {
			return fmt.Sprintf("Reviewing target: %s", args["target"]), nil
		},
	})

	// 1. prompts/list
	reqList := `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}` + "\n"
	var outList bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqList), &outList)

	var respList tmcp.JSONRPCResponse
	_ = json.Unmarshal(outList.Bytes(), &respList)
	prompts := respList.Result.(map[string]any)["prompts"].([]any)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}

	// 2. prompts/get
	reqGet := `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"review-code","arguments":{"target":"pkg/auth"}}}` + "\n"
	var outGet bytes.Buffer
	_ = srv.Serve(context.Background(), strings.NewReader(reqGet), &outGet)

	var respGet tmcp.JSONRPCResponse
	_ = json.Unmarshal(outGet.Bytes(), &respGet)
	messages := respGet.Result.(map[string]any)["messages"].([]any)
	msgContent := messages[0].(map[string]any)["content"].(map[string]any)["text"].(string)

	if msgContent != "Reviewing target: pkg/auth" {
		t.Fatalf("unexpected prompt get response: %s", msgContent)
	}
}

func TestTMCP_AsAction(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("test-mcp", "1.0")
	act := srv.AsAction().Build()

	if act.Describe().Name != "transport.mcp.test-mcp" {
		t.Fatalf("unexpected action name: %s", act.Describe().Name)
	}
}

func TestTMCP_Serve_HandlesUnterminatedFinalLine(t *testing.T) {
	t.Parallel()

	addAct := action.New("math.add", func(ctx context.Context, req AddReq) (AddRes, error) {
		return AddRes{Sum: req.A + req.B}, nil
	}).Build()

	srv := tmcp.New("math-mcp", "1.0.0")
	srv.Mount([]action.AnyAction{addAct})

	// Note: no trailing "\n" on this request.
	reqCall := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"math.add","arguments":{"a":2,"b":3}}}`
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(reqCall), &out); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if out.Len() == 0 {
		t.Fatal("expected a response for the final unterminated line, got none")
	}
}

func TestTMCP_SSESessionDelivery(t *testing.T) {
	t.Parallel()

	addAct := action.New("math.add", func(ctx context.Context, req AddReq) (AddRes, error) {
		return AddRes{Sum: req.A + req.B}, nil
	}).Build()

	srv := tmcp.New("math-mcp", "1.0.0")
	srv.Mount([]action.AnyAction{addAct})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sseReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp/sse", nil)
	sseResp, err := client.Do(sseReq)
	if err != nil {
		t.Fatalf("sse connect failed: %v", err)
	}
	defer sseResp.Body.Close()

	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /mcp/sse, got %d", sseResp.StatusCode)
	}

	reader := bufio.NewReader(sseResp.Body)

	var endpoint string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream closed before endpoint event arrived: %v", err)
		}
		if strings.HasPrefix(line, "data:") {
			endpoint = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	sessionURL := ts.URL + endpoint

	go func() {
		body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"math.add","arguments":{"a":4,"b":5}}}`)
		postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL, body)
		if err != nil {
			t.Errorf("failed to build post request: %v", err)
			return
		}
		postReq.Header.Set("Content-Type", "application/json")

		postResp, err := client.Do(postReq)
		if err != nil {
			t.Errorf("post to session url failed: %v", err)
			return
		}
		defer postResp.Body.Close()
		if postResp.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(postResp.Body)
			t.Errorf("expected 202 Accepted, got %d body=%s", postResp.StatusCode, string(b))
		}
	}()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream closed before response arrived: %v", err)
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var resp tmcp.JSONRPCResponse
		if err := json.Unmarshal([]byte(payload), &resp); err != nil {
			continue
		}
		resMap, ok := resp.Result.(map[string]any)
		if !ok {
			continue
		}
		content, ok := resMap["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		text, _ := content[0].(map[string]any)["text"].(string)

		var sumRes AddRes
		if err := json.Unmarshal([]byte(text), &sumRes); err == nil && sumRes.Sum == 9 {
			return // success
		}
	}
}

type SchemaAddress struct {
	City string `json:"city"`
	Zip  string `json:"zip"`
}

type SchemaOrderReq struct {
	CustomerID string          `json:"customer_id" validate:"required"`
	ShipTo     SchemaAddress   `json:"ship_to"`
	Tags       []string        `json:"tags"`
	Items      []SchemaAddress `json:"items"`
}

func TestBuildJSONSchema_NestedStructsAndSlices(t *testing.T) {
	t.Parallel()

	orderAct := action.New("order.create", func(ctx context.Context, req SchemaOrderReq) (struct{}, error) {
		return struct{}{}, nil
	}).Build()

	srv := tmcp.New("schema-mcp", "1.0.0")
	srv.Mount([]action.AnyAction{orderAct})

	reqList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(reqList), &out); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	var resp tmcp.JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	tools := resp.Result.(map[string]any)["tools"].([]any)
	schema := tools[0].(map[string]any)["inputSchema"].(map[string]any)
	properties := schema["properties"].(map[string]any)

	// ship_to should be a full nested object schema, not "string".
	shipTo := properties["ship_to"].(map[string]any)
	if shipTo["type"] != "object" {
		t.Fatalf("expected ship_to to be type object, got: %+v", shipTo)
	}
	shipToProps := shipTo["properties"].(map[string]any)
	if _, ok := shipToProps["city"]; !ok {
		t.Fatalf("expected ship_to.properties.city to be present, got: %+v", shipToProps)
	}

	// items should be an array whose items schema is an object with city/zip.
	items := properties["items"].(map[string]any)
	if items["type"] != "array" {
		t.Fatalf("expected items to be type array, got: %+v", items)
	}
	itemsElem := items["items"].(map[string]any)
	if itemsElem["type"] != "object" {
		t.Fatalf("expected items element type object, got: %+v", itemsElem)
	}

	// tags should be an array of strings.
	tags := properties["tags"].(map[string]any)
	tagsElem := tags["items"].(map[string]any)
	if tagsElem["type"] != "string" {
		t.Fatalf("expected tags element type string, got: %+v", tagsElem)
	}
}
