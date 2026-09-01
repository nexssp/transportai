package ta2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

type ClientOption func(*Client)

type Client struct {
	baseURL     string
	client      *http.Client
	bearerToken string
}

func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.client = httpClient
		}
	}
}

func WithBearerToken(token string) ClientOption {
	return func(c *Client) {
		c.bearerToken = token
	}
}

func (c *Client) Card(ctx context.Context) (AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/.well-known/agent-card.json", http.NoBody)
	if err != nil {
		return AgentCard{}, err
	}
	c.applyHeaders(req)

	var card AgentCard
	if err := c.doJSON(req, &card); err != nil {
		return AgentCard{}, err
	}
	return card, nil
}

func (c *Client) Send(ctx context.Context, msg Message) (Task, error) {
	data, err := json.Marshal(map[string]any{"message": msg})
	if err != nil {
		return Task{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/message/send", bytes.NewReader(data))
	if err != nil {
		return Task{}, err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	var task Task
	if err := c.doJSON(req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) SendStream(ctx context.Context, msg Message, onEvent func(event string, data []byte) error) error {
	data, err := json.Marshal(map[string]any{"message": msg})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/message/stream", bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	streamClient := *c.client
	streamClient.Timeout = 0

	resp, err := streamClient.Do(req)
	if err != nil {
		return xerr.Unavailable("failed connecting to remote A2A stream", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return xerr.Internal(fmt.Sprintf("remote A2A stream returned HTTP %d: %s", resp.StatusCode, string(body)))
	}

	reader := bufio.NewReader(resp.Body)
	var currentEvent string
	var currentData bytes.Buffer

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if currentEvent != "" || currentData.Len() > 0 {
					if onEvent != nil {
						return onEvent(currentEvent, bytes.TrimSuffix(currentData.Bytes(), []byte("\n")))
					}
				}
				return nil
			}
			return err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if currentEvent != "" || currentData.Len() > 0 {
				if onEvent != nil {
					payload := bytes.TrimSuffix(currentData.Bytes(), []byte("\n"))
					if err := onEvent(currentEvent, payload); err != nil {
						return err
					}
				}
				currentEvent = ""
				currentData.Reset()
			}
			continue
		}

		if after, ok := strings.CutPrefix(line, "event:"); ok {
			currentEvent = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "data:"); ok {
			currentData.WriteString(strings.TrimSpace(after))
			currentData.WriteByte('\n')
		}
	}
}

func (c *Client) Get(ctx context.Context, id string) (Task, error) {
	data, _ := json.Marshal(map[string]string{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tasks/get", bytes.NewReader(data))
	if err != nil {
		return Task{}, err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	var task Task
	if err := c.doJSON(req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	data, _ := json.Marshal(map[string]string{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tasks/cancel", bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	var task Task
	return c.doJSON(req, &task)
}

func (c *Client) AsAction(actionName, role string) *action.Builder[Message, Task] {
	return action.New(actionName, func(ctx context.Context, msg Message) (Task, error) {
		msg.Role = role
		return c.Send(ctx, msg)
	}).Tag("remote", "a2a", "client")
}

func (c *Client) applyHeaders(req *http.Request) {
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
}

func (c *Client) doJSON(req *http.Request, target any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return xerr.Unavailable("remote A2A agent unreachable", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return xerr.Internal("failed reading A2A response", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp xerr.ErrorResponse
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.Error != "" {
			if appErr, ok := xerr.FromPublic(errResp); ok {
				return appErr
			}
		}
		return xerr.Internal(fmt.Sprintf("A2A remote agent returned HTTP %d: %s", resp.StatusCode, string(body)))
	}

	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			return xerr.Internal("failed decoding A2A JSON payload", err)
		}
	}
	return nil
}
