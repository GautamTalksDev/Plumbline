package crawler

import (
	"context"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// observeHTTPAllowlisted connects to an HTTP MCP remote without executing
// stranger binaries. Only public tool metadata is fetched.
func observeHTTPAllowlisted(ctx context.Context, t Target) Observation {
	if t.URL == "" {
		return Observation{ServerID: t.ServerID, EventType: EventRefused, Detail: "no URL"}
	}
	if t.AllowHost == "" {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: "AllowHost required"}
	}
	sess, err := mcpclient.Connect(ctx, mcpclient.ServerSpec{
		Transport: mcpclient.TransportHTTP,
		URL:       t.URL,
	})
	if err != nil {
		ev, detail := MapEvent(err)
		return Observation{ServerID: t.ServerID, EventType: ev, Detail: detail}
	}
	defer sess.Close()
	list, err := sess.ListTools(ctx)
	if err != nil {
		ev, detail := MapEvent(err)
		return Observation{ServerID: t.ServerID, EventType: ev, Detail: detail}
	}
	pv := list.ProtocolVersion
	if pv == "" {
		pv = sess.ProtocolVersion
	}
	return Observation{
		ServerID:        t.ServerID,
		EventType:       EventOK,
		ProtocolVersion: pv,
		Tools:           list.Tools,
	}
}
