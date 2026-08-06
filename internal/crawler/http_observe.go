package crawler

import (
	"context"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// observeHTTPAllowlisted connects to an HTTP MCP remote without executing
// stranger binaries. Only public tool metadata is fetched.
func observeHTTPAllowlisted(ctx context.Context, t Target) Observation {
	if t.URL == "" {
		return observationFromClassified(t.ServerID, Classified{
			Event: EventRefused, Diagnostic: DiagNoTarget,
		})
	}
	if t.AllowHost == "" {
		return observationFromClassified(t.ServerID, Classified{
			Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed,
		})
	}
	sess, err := mcpclient.Connect(ctx, mcpclient.ServerSpec{
		Transport: mcpclient.TransportHTTP,
		URL:       t.URL,
	})
	if err != nil {
		return observationFromClassified(t.ServerID, Classify(err))
	}
	defer func() { _ = sess.Close() }()
	list, err := sess.ListTools(ctx)
	if err != nil {
		return observationFromClassified(t.ServerID, Classify(err))
	}
	pv := list.ProtocolVersion
	if pv == "" {
		pv = sess.ProtocolVersion
	}
	obs := observationFromClassified(t.ServerID, Classified{Event: EventOK, Diagnostic: DiagOK})
	obs.ProtocolVersion = pv
	obs.Tools = list.Tools
	return obs
}
