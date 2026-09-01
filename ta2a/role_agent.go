package ta2a

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

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
		DefaultInputModes:  []string{"text", "data"},
		DefaultOutputModes: []string{"text", "data"},
	}, nil
}

func (t *Transport) Send(ctx context.Context, msg Message) (Task, error) {
	if err := msg.Validate(); err != nil {
		return Task{}, err
	}

	t.mu.RLock()
	act, ok := t.actions[msg.Role]
	t.mu.RUnlock()

	if !ok {
		return Task{}, xerr.NotFound("no agent for role: " + msg.Role)
	}

	exec, ok := act.(action.Executable)
	if !ok {
		return Task{}, xerr.Internal("action is not executable")
	}

	effectiveText := normalizeMessageText(msg)
	if msg.Text == "" && effectiveText != "" {
		msg.Text = effectiveText
	}

	// 1. O(1) indexed Task lookup for ContextID / HITL resumption
	var existingTask *Task
	var taskID string

	if msg.ContextID != "" {
		t.taskMu.RLock()
		if id, found := t.tasksByContext[msg.ContextID]; found {
			if tsk, ok := t.tasks[id]; ok {
				cp := tsk
				existingTask = &cp
				taskID = id
			}
		} else if tsk, ok := t.tasks[msg.ContextID]; ok {
			cp := tsk
			existingTask = &cp
			taskID = msg.ContextID
		}
		t.taskMu.RUnlock()
	}

	if existingTask == nil {
		if preID, ok := msg.Metadata["task_id"].(string); ok && preID != "" {
			taskID = preID
		} else {
			taskID = fmt.Sprintf("task-%d", t.taskSeq.Add(1))
		}
	}

	var history []Message
	var transitions []StateTransition
	callbackURL := msg.CallbackURL

	if existingTask != nil {
		history = append(slices.Clone(existingTask.History), msg)
		transitions = append(slices.Clone(existingTask.Transitions), StateTransition{
			From:      existingTask.Status,
			To:        TaskStatusWorking,
			Timestamp: time.Now().UTC(),
			Reason:    "task resumed with input",
		})
		if callbackURL == "" {
			callbackURL = existingTask.CallbackURL
		}
	} else {
		history = []Message{msg}
		transitions = []StateTransition{
			{From: "", To: TaskStatusWorking, Timestamp: time.Now().UTC(), Reason: "task started"},
		}
	}

	// 2. Execute Decoded Action
	res, err := exec.ExecuteDecoded(ctx, func(target any) error {
		if v, ok := target.(*Message); ok {
			*v = msg
			return nil
		}
		if s, ok := target.(*string); ok {
			*s = effectiveText
			return nil
		}
		if p, ok := target.(*[]Part); ok {
			*p = msg.Parts
			return nil
		}
		data, _ := json.Marshal(msg)
		return json.Unmarshal(data, target)
	})

	finalStatus := TaskStatusCompleted
	var finalTaskText string
	var finalArtifacts []Artifact
	var taskErr *TaskError

	if err != nil {
		finalStatus = TaskStatusFailed
		appErr := xerr.From(err)
		taskErr = &TaskError{
			Code:    string(appErr.Kind),
			Message: appErr.Message,
		}
		finalTaskText = appErr.Error()
	} else {
		if directTask, isTask := res.(Task); isTask {
			finalStatus = directTask.Status
			if finalStatus == "" {
				finalStatus = TaskStatusCompleted
			}
			finalTaskText = directTask.Text
			finalArtifacts = directTask.Artifacts
		} else {
			finalTaskText = formatAgentResult(res)
			finalArtifacts = artifactsFromResult(res)
		}
	}

	transitions = append(transitions, StateTransition{
		From:      TaskStatusWorking,
		To:        finalStatus,
		Timestamp: time.Now().UTC(),
		Reason:    "action execution finished",
	})

	task := Task{
		ID:          taskID,
		ContextID:   msg.ContextID,
		Status:      finalStatus,
		State:       string(finalStatus),
		Text:        finalTaskText,
		Artifacts:   finalArtifacts,
		History:     history,
		Error:       taskErr,
		Transitions: transitions,
		CallbackURL: callbackURL,
	}

	t.taskMu.Lock()
	t.tasks[taskID] = task
	if msg.ContextID != "" {
		t.tasksByContext[msg.ContextID] = taskID
	}
	t.taskMu.Unlock()

	// 3. Webhook dispatch with context, retry, backoff & HMAC signing
	if callbackURL != "" {
		go t.dispatchWebhook(ctx, callbackURL, task)
	}

	if err != nil {
		return task, err
	}
	return task, nil
}

func (t *Transport) Get(_ context.Context, id string) (Task, error) {
	t.taskMu.RLock()
	defer t.taskMu.RUnlock()

	task, ok := t.tasks[id]
	if !ok {
		return Task{}, xerr.NotFound("task not found: " + id)
	}

	return task, nil
}

func (t *Transport) Cancel(ctx context.Context, id string) error {
	t.taskMu.Lock()
	defer t.taskMu.Unlock()

	task, ok := t.tasks[id]
	if !ok {
		return xerr.NotFound("task not found: " + id)
	}

	if task.Status == TaskStatusCanceled {
		return nil
	}

	task.Transitions = append(task.Transitions, StateTransition{
		From:      task.Status,
		To:        TaskStatusCanceled,
		Timestamp: time.Now().UTC(),
		Reason:    "task canceled by client",
	})
	task.Status = TaskStatusCanceled
	task.State = string(TaskStatusCanceled)
	t.tasks[id] = task

	if task.CallbackURL != "" {
		go t.dispatchWebhook(ctx, task.CallbackURL, task)
	}

	return nil
}

func normalizeMessageText(msg Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	if len(msg.Parts) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, p := range msg.Parts {
		partText := ""
		switch p.Type {
		case PartText:
			partText = p.Text
		case PartFile:
			if p.File != nil {
				if p.File.URL != "" {
					partText = p.File.URL
				} else {
					partText = p.File.Name
				}
			}
		case PartData:
			if len(p.Data) > 0 {
				if data, err := json.Marshal(p.Data); err == nil {
					partText = string(data)
				}
			}
		}

		if partText != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(partText)
		}
	}

	return sb.String()
}

func artifactsFromResult(res any) []Artifact {
	switch v := res.(type) {
	case Artifact:
		return []Artifact{v}
	case *Artifact:
		if v != nil {
			return []Artifact{*v}
		}
	case []Artifact:
		return v
	case []*Artifact:
		out := make([]Artifact, 0, len(v))
		for _, a := range v {
			if a != nil {
				out = append(out, *a)
			}
		}
		return out
	}
	return nil
}

func formatAgentResult(res any) string {
	if res == nil {
		return ""
	}

	switch v := res.(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
		return ""
	case []byte:
		return string(v)
	case encoding.TextMarshaler:
		if b, err := v.MarshalText(); err == nil {
			return string(b)
		}
	case fmt.Stringer:
		return v.String()
	}

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", res)
	}
	return string(b)
}

func (t *Transport) dispatchWebhook(ctx context.Context, url string, task Task) {
	data, err := json.Marshal(task)
	if err != nil {
		return
	}

	client := t.webhookClient
	if client == nil {
		client = http.DefaultClient
	}

	baseCtx := context.WithoutCancel(ctx)

	for attempt := 1; attempt <= 3; attempt++ {
		reqCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			cancel()
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-A2A-Delivery-Attempt", fmt.Sprintf("%d", attempt))

		if t.webhookSecret != "" {
			mac := hmac.New(sha256.New, []byte(t.webhookSecret))
			mac.Write(data)
			req.Header.Set("X-A2A-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}

		resp, err := client.Do(req)
		cancel()
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}

		if attempt < 3 {
			time.Sleep(time.Duration(attempt*50) * time.Millisecond)
		}
	}

	slog.Warn("a2a_webhook_delivery_exhausted", "url", url, "task_id", task.ID)
}
