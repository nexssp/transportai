package ta2a

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nexssp/kernel/xerr"
)

type AuthVerifier interface {
	Verify(ctx context.Context, r *http.Request) (context.Context, error)
}

type AuthVerifierFunc func(ctx context.Context, r *http.Request) (context.Context, error)

func (f AuthVerifierFunc) Verify(ctx context.Context, r *http.Request) (context.Context, error) {
	return f(ctx, r)
}

type AgentCardProvider interface {
	AgentCard(ctx context.Context) (AgentCard, error)
}

type AgentCardProviderFunc func(ctx context.Context) (AgentCard, error)

func (f AgentCardProviderFunc) AgentCard(ctx context.Context) (AgentCard, error) {
	return f(ctx)
}

type AgentCard struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	URL                string          `json:"url,omitempty"`
	Version            string          `json:"version,omitempty"`
	Capabilities       map[string]bool `json:"capabilities,omitempty"`
	DefaultInputModes  []string        `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string        `json:"defaultOutputModes,omitempty"`
	Skills             []string        `json:"skills,omitempty"`
}

type PartType string

const (
	PartText PartType = "text"
	PartFile PartType = "file"
	PartData PartType = "data"
)

type FilePart struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URL      string `json:"url,omitempty"`
	Content  []byte `json:"content,omitempty"`
}

type Part struct {
	Type PartType       `json:"type"`
	Text string         `json:"text,omitempty"`
	Data map[string]any `json:"data,omitempty"`
	File *FilePart      `json:"file,omitempty"`
}

func (p Part) HasContent() bool {
	if strings.TrimSpace(p.Text) != "" {
		return true
	}
	if len(p.Data) > 0 {
		return true
	}
	if p.File != nil && (p.File.URL != "" || p.File.Name != "" || len(p.File.Content) > 0) {
		return true
	}
	return false
}

type Message struct {
	Role        string         `json:"role"`
	Text        string         `json:"text,omitempty"`
	Parts       []Part         `json:"parts,omitempty"`
	ContextID   string         `json:"contextId,omitempty"`
	CallbackURL string         `json:"callbackUrl,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func (m Message) Validate() error {
	if m.Role == "" {
		return xerr.BadRequest("message role is required")
	}

	if strings.TrimSpace(m.Text) != "" {
		return nil
	}

	for _, p := range m.Parts {
		if p.HasContent() {
			return nil
		}
	}

	return xerr.BadRequest("message text or parts is required")
}

type Artifact struct {
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URL      string `json:"url,omitempty"`
	Data     any    `json:"data,omitempty"`
}

type ArtifactProvider interface {
	Artifacts() []Artifact
}

type TaskStatus string

const (
	TaskStatusSubmitted     TaskStatus = "submitted"
	TaskStatusWorking       TaskStatus = "working"
	TaskStatusInputRequired TaskStatus = "input-required"
	TaskStatusCompleted     TaskStatus = "completed"
	TaskStatusCanceled      TaskStatus = "canceled"
	TaskStatusFailed        TaskStatus = "failed"
	TaskStatusRejected      TaskStatus = "rejected"
)

type StateTransition struct {
	From      TaskStatus `json:"from"`
	To        TaskStatus `json:"to"`
	Timestamp time.Time  `json:"timestamp"`
	Reason    string     `json:"reason,omitempty"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type Task struct {
	ID          string            `json:"id"`
	ContextID   string            `json:"contextId,omitempty"`
	Status      TaskStatus        `json:"status"`
	State       string            `json:"state,omitempty"`
	Text        string            `json:"text,omitempty"`
	Artifacts   []Artifact        `json:"artifacts,omitempty"`
	History     []Message         `json:"history,omitempty"`
	Error       *TaskError        `json:"error,omitempty"`
	Transitions []StateTransition `json:"transitions,omitempty"`
	CallbackURL string            `json:"callbackUrl,omitempty"`
}

type Agent interface {
	Card(context.Context) (AgentCard, error)
	Send(context.Context, Message) (Task, error)
	Get(context.Context, string) (Task, error)
	Cancel(context.Context, string) error
}

type StreamYield func(chunk string) error
type StreamArtifactYield func(artifact Artifact) error

type streamContextKey struct{}
type streamArtifactContextKey struct{}

func WithStreamYield(ctx context.Context, fn StreamYield) context.Context {
	return context.WithValue(ctx, streamContextKey{}, fn)
}

func YieldToken(ctx context.Context, chunk string) error {
	if fn, ok := ctx.Value(streamContextKey{}).(StreamYield); ok && fn != nil {
		return fn(chunk)
	}
	return nil
}

func WithStreamArtifactYield(ctx context.Context, fn StreamArtifactYield) context.Context {
	return context.WithValue(ctx, streamArtifactContextKey{}, fn)
}

func YieldArtifact(ctx context.Context, art Artifact) error {
	if fn, ok := ctx.Value(streamArtifactContextKey{}).(StreamArtifactYield); ok && fn != nil {
		return fn(art)
	}
	return nil
}
