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
	"reflect"
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

func (t *Transport) Send(ctx context.Context, msg Message) (Task, error) {
	if err := msg.Validate(); err != nil {
		return Task{}, err
	}

	t.mu.RLock()
	act, ok := t.actions[msg.Role]
	binding, hasBinding := t.bindings[msg.Role]
	t.mu.RUnlock()

	if !ok {
		return Task{}, xerr.NotFound("no agent for role: " + msg.Role)
	}

	exec, ok := act.(action.Executable)
	if !ok {
		return Task{}, xerr.Internal("action is not executable")
	}

	// 1. Task lookup & State transition baseline
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
		transitions = slices.Clone(existingTask.Transitions)
		if callbackURL == "" {
			callbackURL = existingTask.CallbackURL
		}
		transitions = append(transitions, StateTransition{
			From:      existingTask.Status,
			To:        TaskStatusWorking,
			Timestamp: time.Now().UTC(),
			Reason:    "task resumed with input",
		})
	} else {
		history = []Message{msg}
		transitions = []StateTransition{
			{From: "", To: TaskStatusWorking, Timestamp: time.Now().UTC(), Reason: "task started"},
		}
	}

	// 2. Built-in Stateful HITL Interceptor
	execMsg := msg
	approvalKey := msg.ContextID
	if approvalKey == "" {
		approvalKey = taskID
	}

	if hasBinding && binding.HITL != nil {
		cleanText := strings.TrimSpace(msg.Text)

		// A. Check trigger words for pauses
		if existingTask == nil || existingTask.Status != TaskStatusInputRequired {
			for _, trigger := range binding.HITL.TriggerWords {
				if cleanText != trigger {
					continue
				}

				transitions = append(transitions, StateTransition{
					From:      TaskStatusWorking,
					To:        TaskStatusInputRequired,
					Timestamp: time.Now().UTC(),
					Reason:    "human approval required",
				})

				t.taskMu.Lock()
				t.pendingApprovals[approvalKey] = pendingApprovalEntry{
					msg:       msg,
					createdAt: time.Now().UTC(),
				}
				t.taskMu.Unlock()

				hitlTask := Task{
					ID:        taskID,
					ContextID: msg.ContextID,
					Status:    TaskStatusInputRequired,
					State:     string(TaskStatusInputRequired),
					Text:      binding.HITL.Prompt,
					Artifacts: []Artifact{
						{
							Name: "ApprovalForm",
							Type: "form",
							Data: map[string]any{
								"prompt":  binding.HITL.Prompt,
								"options": binding.HITL.Options,
							},
						},
					},
					History:     history,
					Transitions: transitions,
					CallbackURL: callbackURL,
				}

				t.recordTask(hitlTask)
				return hitlTask, nil
			}
		}

		// B. Handle Rejection
		if cleanText == "reject" {
			prevStatus := TaskStatusWorking
			if existingTask != nil {
				prevStatus = existingTask.Status
			}

			transitions = append(transitions, StateTransition{
				From:      prevStatus,
				To:        TaskStatusRejected,
				Timestamp: time.Now().UTC(),
				Reason:    "operation rejected by operator",
			})

			t.taskMu.Lock()
			delete(t.pendingApprovals, approvalKey)
			t.taskMu.Unlock()

			rejectedTask := Task{
				ID:          taskID,
				ContextID:   msg.ContextID,
				Status:      TaskStatusRejected,
				State:       string(TaskStatusRejected),
				Text:        "Operation rejected by operator.",
				History:     history,
				Transitions: transitions,
				CallbackURL: callbackURL,
			}
			t.recordTask(rejectedTask)
			return rejectedTask, nil
		}

		// C. Handle Approval & Resumption
		if cleanText == "approve" {
			t.taskMu.Lock()
			origEntry, hasOrig := t.pendingApprovals[approvalKey]
			delete(t.pendingApprovals, approvalKey)
			t.taskMu.Unlock()

			if !hasOrig {
				return Task{}, xerr.Conflict("no pending approval found for context: " + approvalKey)
			}

			execMsg = origEntry.msg
			if msg.CallbackURL != "" {
				execMsg.CallbackURL = msg.CallbackURL
			}
		}
	}

	effectiveText := normalizeMessageText(execMsg)
	if execMsg.Text == "" && effectiveText != "" {
		execMsg.Text = effectiveText
	}

	var decodedTarget any

	// 3. Execute Decoded Action with strict Decoder Precedence
	// Precedence: DefaultArgs -> Message Parts/Data -> Text Override -> Hard Override Args
	res, err := exec.ExecuteDecoded(ctx, func(target any) error {
		decodedTarget = target

		if v, ok := target.(*Message); ok {
			*v = execMsg
			return nil
		}
		if s, ok := target.(*string); ok {
			*s = effectiveText
			return nil
		}
		if p, ok := target.(*[]Part); ok {
			*p = execMsg.Parts
			return nil
		}

		// Step 1: DefaultArgs (fallback values)
		if hasBinding && binding.DefaultArgs != nil {
			mergeDefaults(target, binding.DefaultArgs)
		}

		// Step 2: Message Parts/Data (structured client input)
		if len(execMsg.Parts) > 0 {
			for _, part := range execMsg.Parts {
				if part.Type == PartData && len(part.Data) > 0 {
					mergeOverrides(target, part.Data)
				}
			}
		}

		// Step 3: Text shorthand (e.g. text -> Profile/Text/Input/Query)
		if effectiveText != "" && target != nil {
			applyTextToStruct(target, effectiveText)
		}

		// Step 4: Hard Override Args (strictly enforced overrides)
		if hasBinding && binding.Args != nil {
			mergeOverrides(target, binding.Args)
		}

		return nil
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
			finalArtifacts = autoExtractArtifacts(decodedTarget, res, binding)

			if hasBinding && binding.SummaryTemplate != "" {
				finalTaskText = evaluateCompositeTemplate(binding.SummaryTemplate, decodedTarget, res)
			}
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

	t.recordTask(task)

	if callbackURL != "" {
		go t.dispatchWebhook(ctx, callbackURL, task)
	}

	if err != nil {
		return task, err
	}
	return task, nil
}

func mergeDefaults(dst any, src any) {
	if src == nil || dst == nil {
		return
	}

	srcBytes, err := json.Marshal(src)
	if err != nil {
		return
	}

	var srcMap map[string]any
	if err = json.Unmarshal(srcBytes, &srcMap); err != nil {
		return
	}

	dstBytes, err := json.Marshal(dst)
	if err != nil {
		return
	}

	var dstMap map[string]any
	if err = json.Unmarshal(dstBytes, &dstMap); err != nil {
		dstMap = make(map[string]any)
	}

	for k, v := range srcMap {
		if _, exists := dstMap[k]; !exists || isZeroValue(dstMap[k]) {
			dstMap[k] = v
		}
	}

	mergedBytes, err := json.Marshal(dstMap)
	if err == nil {
		_ = json.Unmarshal(mergedBytes, dst)
	}
}

func mergeOverrides(dst any, src any) {
	if src == nil || dst == nil {
		return
	}

	srcBytes, err := json.Marshal(src)
	if err != nil {
		return
	}

	var srcMap map[string]any
	if err = json.Unmarshal(srcBytes, &srcMap); err != nil {
		return
	}

	dstBytes, err := json.Marshal(dst)
	if err != nil {
		return
	}

	var dstMap map[string]any
	if err = json.Unmarshal(dstBytes, &dstMap); err != nil {
		dstMap = make(map[string]any)
	}

	for k, v := range srcMap {
		dstMap[k] = v
	}

	mergedBytes, err := json.Marshal(dstMap)
	if err == nil {
		_ = json.Unmarshal(mergedBytes, dst)
	}
}

func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case float64:
		return val == 0
	case bool:
		return !val
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	}
	return false
}

func (t *Transport) recordTask(task Task) {
	t.taskMu.Lock()
	defer t.taskMu.Unlock()

	t.tasks[task.ID] = task
	if task.ContextID != "" {
		t.tasksByContext[task.ContextID] = task.ID
	}
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
		// PartData is strictly structured input — do NOT convert to text shorthand
		case PartData:
			continue
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

func autoExtractArtifacts(req any, res any, binding AgentBinding) []Artifact {
	if res == nil {
		return nil
	}

	if provider, ok := res.(ArtifactProvider); ok {
		return provider.Artifacts()
	}

	existing := artifactsFromResult(res)
	if len(existing) > 0 {
		return existing
	}

	content, hasContent := extractStructField(res, "Content")
	if hasContent && content != "" {
		name := binding.ArtifactName
		if name != "" {
			name = evaluateCompositeTemplate(name, req, res)
		} else {
			profile, _ := extractStructField(res, "Profile")
			if profile == "" {
				profile, _ = extractStructField(req, "Profile")
			}
			if profile == "" {
				profile = "default"
			}
			name = fmt.Sprintf("context_%s.md", profile)
		}

		mimeType := binding.ArtifactMime
		if mimeType == "" {
			mimeType = "text/markdown"
		}

		return []Artifact{
			{
				Name:     name,
				Type:     "file",
				MimeType: mimeType,
				Data:     content,
			},
		}
	}

	return nil
}

func extractStructField(obj any, fieldName string) (string, bool) {
	if obj == nil {
		return "", false
	}
	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return "", false
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return "", false
	}

	f := val.FieldByName(fieldName)
	if !f.IsValid() {
		return "", false
	}

	switch f.Kind() {
	case reflect.String:
		return f.String(), true
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.Uint8 {
			return string(f.Bytes()), true
		}
	}
	return "", false
}

func applyTextToStruct(target any, text string) {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return
	}
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}

	for _, name := range []string{"Profile", "Text", "Input", "Query"} {
		f := elem.FieldByName(name)
		if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
			f.SetString(text)
			return
		}
	}
}

func evaluateCompositeTemplate(tmpl string, sources ...any) string {
	if tmpl == "" {
		return tmpl
	}

	combined := make(map[string]any)
	for _, src := range sources {
		if src == nil {
			continue
		}

		val := reflect.ValueOf(src)
		if val.Kind() == reflect.Pointer && !val.IsNil() {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			typ := val.Type()
			for i := 0; i < val.NumField(); i++ {
				field := typ.Field(i)
				combined[field.Name] = val.Field(i).Interface()
			}
		}

		if d, err := json.Marshal(src); err == nil {
			var m map[string]any
			if json.Unmarshal(d, &m) == nil {
				for k, v := range m {
					combined[k] = v
				}
			}
		}
	}

	out := tmpl
	for k, v := range combined {
		valStr := fmt.Sprintf("%v", v)

		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", k), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToUpper(k)), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToLower(k)), valStr)

		noUnderscore := strings.ReplaceAll(k, "_", "")
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToUpper(noUnderscore)), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToLower(noUnderscore)), valStr)
	}
	return out
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

	val := reflect.ValueOf(res)
	if val.Kind() == reflect.Pointer && val.IsNil() {
		return ""
	}

	if s, ok := res.(string); ok {
		return s
	}
	if s, ok := res.(*string); ok {
		if s != nil {
			return *s
		}
		return ""
	}
	if b, ok := res.([]byte); ok {
		return string(b)
	}

	if tm, ok := res.(encoding.TextMarshaler); ok {
		if b, err := tm.MarshalText(); err == nil {
			return string(b)
		}
	}

	if s, ok := extractStructField(res, "Summary"); ok && s != "" {
		return s
	}

	if _, hasContent := extractStructField(res, "Content"); !hasContent {
		if st, ok := res.(fmt.Stringer); ok {
			return st.String()
		}
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
