package ta2a

import (
	"context"
	"net/http"

	"github.com/nexssp/transport/identity"
)

type a2aSource struct{ headers map[string]string }

func (s a2aSource) Header(name string) string {
	switch name {
	case identity.HeaderRequestID, identity.HeaderExecutionID, identity.HeaderTraceID, identity.HeaderSpanID:
		return s.headers[name]
	default:
		// Message metadata is caller-controlled; authorization identity is not trusted here.
		return ""
	}
}

type a2aWriter struct{ msg *Message }

func (w a2aWriter) SetHeader(name, value string) {
	if w.msg == nil {
		return
	}
	w.msg.SetMetadataString(name, value)
}

type a2aHTTPWriter struct{ headers http.Header }

func (w a2aHTTPWriter) SetHeader(name, value string) {
	if w.headers != nil {
		w.headers.Set(name, value)
	}
}

func populateFromMessage(ctx context.Context, msg Message) context.Context {
	return identity.Populate(ctx, a2aSource{headers: msg.MetadataString()})
}

func propagateToMessage(ctx context.Context, msg *Message) {
	identity.Propagate(ctx, a2aWriter{msg: msg})
}

func propagateCorrelationToHTTP(ctx context.Context, headers http.Header) {
	identity.PropagateCorrelation(ctx, a2aHTTPWriter{headers: headers})
}
