package ta2a

import (
	"net/http"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/thttp"
)

func (t *Transport) HTTPRoutes() []action.AnyAction {
	return []action.AnyAction{
		action.New[any, any]("a2a.agentcard", nil).
			Description("A2A Agent Card Discovery").
			Route(thttp.RawHandler(http.MethodGet, "/.well-known/agent-card.json", t.handleAgentCard)).
			Build(),

		action.New[any, any]("a2a.send", nil).
			Description("A2A Message Send").
			Route(thttp.RawHandler(http.MethodPost, "/message/send", t.handleSend)).
			Build(),

		action.New[any, any]("a2a.stream", nil).
			Description("A2A Message Stream").
			Route(thttp.RawHandler(http.MethodPost, "/message/stream", t.handleStream)).
			Build(),

		action.New[any, any]("a2a.get", nil).
			Description("A2A Task Get").
			Route(thttp.RawHandler(http.MethodPost, "/tasks/get", t.handleGet)).
			Build(),

		action.New[any, any]("a2a.cancel", nil).
			Description("A2A Task Cancel").
			Route(thttp.RawHandler(http.MethodPost, "/tasks/cancel", t.handleCancel)).
			Build(),
	}
}
