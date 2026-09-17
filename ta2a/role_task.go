package ta2a

import (
	"context"
	"time"

	"github.com/nexssp/kernel/xerr"
)

func (t *Transport) recordTaskLocked(task Task) {
	t.tasks[task.ID] = task
	if task.ContextID != "" {
		t.tasksByContext[task.ContextID] = task.ID
	}
}

func (t *Transport) recordTask(task Task) {
	t.taskMu.Lock()
	defer t.taskMu.Unlock()
	t.recordTaskLocked(task)
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
		Reason:    reasonTaskCanceled,
	})
	task.Transitions = trimTransitions(task.Transitions)
	task.Status = TaskStatusCanceled
	task.State = string(TaskStatusCanceled)
	t.tasks[id] = task

	if task.CallbackURL != "" {
		go t.dispatchWebhook(ctx, task.CallbackURL, task)
	}

	return nil
}
