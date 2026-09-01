package ta2a_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/ai/dag"
	"github.com/nexssp/transportai/ta2a"
)

func TestA2A_Client_And_DAG_Federation(t *testing.T) {
	t.Parallel()

	remoteServer := ta2a.New(":0", nil)
	remoteServer.Mount([]action.AnyAction{assistantAction()})

	ts := httptest.NewServer(remoteServer.Handler())
	defer ts.Close()

	client := ta2a.NewClient(ts.URL)

	card, err := client.Card(context.Background())
	if err != nil || card.Name != "nexss-a2a-agent" {
		t.Fatalf("client.Card failed: card=%+v err=%v", card, err)
	}

	remoteNode := client.AsAction("remote.assistant.step", "assistant").Build()

	dagAction := action.New("dag.step", func(ctx context.Context, nCtx *dag.NodeContext) (string, error) {
		tsk, doErr := remoteNode.Do(ctx, ta2a.Message{
			Text: "Federated_DAG_Query",
		})
		if doErr != nil {
			return "", doErr
		}
		return tsk.Text, nil
	}).Build()

	cdag, err := dag.New("federated_agent_dag").
		AddNode("remote_node", "result", dagAction).
		Compile()

	if err != nil {
		t.Fatalf("failed compiling DAG: %v", err)
	}

	state := dag.AcquireState()
	defer state.Release()

	finalState, err := cdag.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("DAG execution failed: %v", err)
	}
	defer finalState.Release()

	res, err := dag.GetNodeOutput[string](finalState, "remote_node")
	if err != nil || res != "Hello: Federated_DAG_Query" {
		t.Fatalf("unexpected DAG result: res=%q err=%v", res, err)
	}
}

func TestA2A_Client_SendStream_CallbackError(t *testing.T) {
	t.Parallel()

	remoteServer := ta2a.New(":0", nil)
	remoteServer.Mount([]action.AnyAction{streamArtifactAction()})

	ts := httptest.NewServer(remoteServer.Handler())
	defer ts.Close()

	client := ta2a.NewClient(ts.URL)

	customErr := errors.New("client abort event processing")
	err := client.SendStream(context.Background(), ta2a.Message{
		Role: "reporter",
		Text: "test",
	}, func(event string, data []byte) error {
		return customErr
	})

	if !errors.Is(err, customErr) {
		t.Fatalf("expected callback error %v, got %v", customErr, err)
	}
}

func TestA2A_Client_SendStream_Success(t *testing.T) {
	t.Parallel()

	remoteServer := ta2a.New(":0", nil)
	remoteServer.Mount([]action.AnyAction{streamArtifactAction()})

	ts := httptest.NewServer(remoteServer.Handler())
	defer ts.Close()

	client := ta2a.NewClient(ts.URL)

	var receivedEvents []string
	err := client.SendStream(context.Background(), ta2a.Message{
		Role: "reporter",
		Text: "ClientStreamTest",
	}, func(event string, data []byte) error {
		receivedEvents = append(receivedEvents, event+":"+string(data))
		return nil
	})

	if err != nil {
		t.Fatalf("client.SendStream failed: %v", err)
	}

	joined := strings.Join(receivedEvents, "\n")
	if !strings.Contains(joined, "status:") || !strings.Contains(joined, "chunk:") || !strings.Contains(joined, "complete:") {
		t.Fatalf("missing expected streamed events in client: %s", joined)
	}
}
