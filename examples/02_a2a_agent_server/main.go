package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/nexssp/transportai/ta2a"
)

type SupportAgent struct {
	mu    sync.Mutex
	tasks map[string]ta2a.Task
}

func (a *SupportAgent) Card(_ context.Context) (ta2a.AgentCard, error) {
	return ta2a.AgentCard{
		Name:        "customer-support-agent",
		Description: "Answers customer queries and delegates refunds",
		Version:     "1.0.0",
		Capabilities: map[string]bool{
			"refunds": true,
			"support": true,
		},
	}, nil
}

func (a *SupportAgent) Send(_ context.Context, message ta2a.Message) (ta2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tasks == nil {
		a.tasks = make(map[string]ta2a.Task)
	}

	taskID := fmt.Sprintf("task-%d", len(a.tasks)+1)
	task := ta2a.Task{
		ID:        taskID,
		ContextID: message.ContextID,
		State:     "completed",
		Text:      "Agent received query: " + message.Text,
	}
	a.tasks[taskID] = task
	return task, nil
}

func (a *SupportAgent) Get(_ context.Context, id string) (ta2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[id]
	if !ok {
		return ta2a.Task{}, fmt.Errorf("task %s not found", id)
	}
	return task, nil
}

func (a *SupportAgent) Cancel(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	task.State = "canceled"
	a.tasks[id] = task
	return nil
}

func main() {
	srv := ta2a.New(":8090", &SupportAgent{})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Println("🤖 Agent2Agent server listening on http://localhost:8090")
	fmt.Println("   Agent Card: http://localhost:8090/.well-known/agent-card.json")

	if _, err := srv.Do(ctx, nil); err != nil {
		fmt.Printf("A2A Server stopped: %v\n", err)
	}
}
