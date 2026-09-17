package ta2a

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

// Bounds keep in-memory task history and transitions under a fixed ceiling.
// Long HITL loops or streams would otherwise grow unbounded.
const (
	maxTaskHistory     = 8
	maxTaskTransitions = 16
)

func (t *Transport) Send(ctx context.Context, msg Message) (Task, error) {
	if err := msg.Validate(); err != nil {
		return Task{}, err
	}

	act, binding, hasBinding, ok := t.lookupRole(msg.Role)
	if !ok {
		return Task{}, xerr.NotFound("no agent for role: " + msg.Role)
	}

	exec, ok := act.(action.Executable)
	if !ok {
		return Task{}, xerr.Internal("action is not executable")
	}

	existingTask, taskID, history, transitions, callbackURL := t.prepareTaskContext(msg)

	approvalKey := msg.ContextID
	if approvalKey == "" {
		approvalKey = taskID
	}

	if hasBinding && binding.HITL != nil {
		effectiveMsg, earlyTask, err := t.applyHITLState(
			msg, binding, existingTask, taskID, approvalKey,
			history, transitions, callbackURL,
		)
		if err != nil {
			return Task{}, err
		}
		if earlyTask != nil {
			return *earlyTask, nil
		}
		msg = effectiveMsg
	}

	effectiveText := normalizeMessageText(msg)
	if msg.Text == "" && effectiveText != "" {
		msg.Text = effectiveText
	}

	res, decodedTarget, execErr := executeWithPrecedence(ctx, exec, msg, binding, effectiveText)

	finalStatus, finalTaskText, finalArtifacts, taskErr := buildTaskResult(res, decodedTarget, execErr, binding)

	transitions = append(transitions, StateTransition{
		From:      TaskStatusWorking,
		To:        finalStatus,
		Timestamp: time.Now().UTC(),
		Reason:    reasonTaskFinished,
	})
	transitions = trimTransitions(transitions)

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

	if execErr != nil {
		return task, execErr
	}
	return task, nil
}

// applyHITLState runs the HITL state machine under a single lock to avoid
// TOCTOU between pause/approve/reject. Returns the effective message to
// execute (which may be the original paused message after approval) and an
// optional terminal task the caller must return as-is.
func (t *Transport) applyHITLState(
	msg Message,
	binding AgentBinding,
	existingTask *Task,
	taskID, approvalKey string,
	history []Message,
	transitions []StateTransition,
	callbackURL string,
) (Message, *Task, error) {
	text := strings.TrimSpace(msg.Text)

	t.taskMu.Lock()
	defer t.taskMu.Unlock()

	pending, isPending := t.pendingApprovals[approvalKey]

	if isPending {
		switch text {
		case HITLApprove:
			delete(t.pendingApprovals, approvalKey)

			resumed := pending.msg
			if msg.CallbackURL != "" {
				resumed.CallbackURL = msg.CallbackURL
			}
			return resumed, nil, nil

		case HITLReject:
			delete(t.pendingApprovals, approvalKey)

			transitions = append(transitions, StateTransition{
				From:      TaskStatusInputRequired,
				To:        TaskStatusRejected,
				Timestamp: time.Now().UTC(),
				Reason:    reasonOperatorReject,
			})

			rejected := Task{
				ID:          taskID,
				ContextID:   msg.ContextID,
				Status:      TaskStatusRejected,
				State:       string(TaskStatusRejected),
				Text:        "Operation rejected by operator.",
				History:     append(history, msg),
				Transitions: trimTransitions(transitions),
				CallbackURL: callbackURL,
			}
			t.recordTaskLocked(rejected)
			return Message{}, &rejected, nil

		default:
			return Message{}, nil, xerr.BadRequest(
				"task is awaiting approval: send 'approve' or 'reject'")
		}
	}

	if text == HITLApprove || text == HITLReject {
		return Message{}, nil, xerr.Conflict(
			"no pending approval for context " + approvalKey)
	}

	if existingTask != nil && existingTask.Status == TaskStatusInputRequired {
		return Message{}, nil, xerr.Conflict(
			"approval for task " + taskID + " has expired; start a new request")
	}

	for _, trigger := range binding.HITL.TriggerWords {
		if text != trigger {
			continue
		}

		transitions = append(transitions, StateTransition{
			From:      TaskStatusWorking,
			To:        TaskStatusInputRequired,
			Timestamp: time.Now().UTC(),
			Reason:    reasonApprovalNeeded,
		})

		t.pendingApprovals[approvalKey] = pendingApprovalEntry{
			msg:       msg,
			createdAt: time.Now().UTC(),
		}

		paused := Task{
			ID:        taskID,
			ContextID: msg.ContextID,
			Status:    TaskStatusInputRequired,
			State:     string(TaskStatusInputRequired),
			Text:      binding.HITL.Prompt,
			Artifacts: []Artifact{{
				Name: "ApprovalForm",
				Type: "form",
				Data: map[string]any{
					"prompt":  binding.HITL.Prompt,
					"options": binding.HITL.Options,
				},
			}},
			History:     append(history, msg),
			Transitions: trimTransitions(transitions),
			CallbackURL: callbackURL,
		}
		t.recordTaskLocked(paused)
		return Message{}, &paused, nil
	}

	return msg, nil, nil
}

// lookupRole returns the action + binding + existence flags for a role.
func (t *Transport) lookupRole(role string) (action.AnyAction, AgentBinding, bool, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	act, ok := t.actions[role]
	binding, hasBinding := t.bindings[role]
	return act, binding, hasBinding, ok
}

// prepareTaskContext resolves the existing task state (if any) and returns
// the baseline history, transitions, and callback URL for a new or resumed task.
func (t *Transport) prepareTaskContext(msg Message) (
	existingTask *Task,
	taskID string,
	history []Message,
	transitions []StateTransition,
	callbackURL string,
) {
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
		if preID, ok := msg.Metadata[metaTaskID].(string); ok && preID != "" {
			taskID = preID
		} else {
			taskID = taskIDPrefix + strconv.FormatUint(t.taskSeq.Add(1), 10)
		}
	}

	callbackURL = msg.CallbackURL

	if existingTask != nil {
		history = make([]Message, 0, maxTaskHistory)
		history = append(history, existingTask.History...)
		history = append(history, msg)

		transitions = make([]StateTransition, 0, maxTaskTransitions)
		transitions = append(transitions, existingTask.Transitions...)

		if callbackURL == "" {
			callbackURL = existingTask.CallbackURL
		}
		transitions = append(transitions, StateTransition{
			From:      existingTask.Status,
			To:        TaskStatusWorking,
			Timestamp: time.Now().UTC(),
			Reason:    reasonTaskResumed,
		})
	} else {
		history = []Message{msg}
		transitions = []StateTransition{
			{From: "", To: TaskStatusWorking, Timestamp: time.Now().UTC(), Reason: reasonTaskStarted},
		}
	}

	if len(history) > maxTaskHistory {
		history = history[len(history)-maxTaskHistory:]
	}

	return existingTask, taskID, history, transitions, callbackURL
}

func trimTransitions(transitions []StateTransition) []StateTransition {
	if len(transitions) > maxTaskTransitions {
		return transitions[len(transitions)-maxTaskTransitions:]
	}
	return transitions
}

// executeWithPrecedence runs the action with the documented argument precedence:
//
//	DefaultArgs -> Message Parts/Data -> Text Override -> Hard Override Args
//
// It returns both the raw result and the decoded request target, so callers
// can later evaluate templates against the actual request struct.
func executeWithPrecedence(
	ctx context.Context,
	exec action.Executable,
	execMsg Message,
	binding AgentBinding,
	effectiveText string,
) (res, decodedTarget any, err error) {
	res, err = exec.ExecuteDecoded(ctx, func(target any) error {
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

		if binding.DefaultArgs != nil {
			mergeDefaults(target, binding.DefaultArgs)
		}

		if len(execMsg.Parts) > 0 {
			for _, part := range execMsg.Parts {
				if part.Type == PartData && len(part.Data) > 0 {
					mergeOverrides(target, part.Data)
				}
			}
		}

		if effectiveText != "" && target != nil {
			applyTextToStruct(target, effectiveText)
		}

		if binding.Args != nil {
			mergeOverrides(target, binding.Args)
		}

		return nil
	})

	return res, decodedTarget, err
}

// buildTaskResult maps the action result (or error) to Task fields.
// decodedTarget is the request struct that was passed to the action — it is
// used for artifact-name templating and SummaryTemplate evaluation.
func buildTaskResult(
	res any,
	decodedTarget any,
	execErr error,
	binding AgentBinding,
) (status TaskStatus, text string, artifacts []Artifact, taskErr *TaskError) {
	if execErr != nil {
		appErr := xerr.From(execErr)
		return TaskStatusFailed, appErr.Error(), nil, &TaskError{
			Code:    string(appErr.Kind),
			Message: appErr.Message,
		}
	}

	if directTask, isTask := res.(Task); isTask {
		s := directTask.Status
		if s == "" {
			s = TaskStatusCompleted
		}
		return s, directTask.Text, directTask.Artifacts, nil
	}

	text = formatAgentResult(res)
	artifacts = autoExtractArtifacts(decodedTarget, res, binding)

	if binding.SummaryTemplate != "" {
		text = evaluateCompositeTemplate(binding.SummaryTemplate, decodedTarget, res)
	}

	return TaskStatusCompleted, text, artifacts, nil
}
