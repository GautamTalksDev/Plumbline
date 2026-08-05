package crawler

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Official registry base URL.
const DefaultRegistryURL = "https://registry.modelcontextprotocol.io"

// UserAgent identifies the project and documents the crawl.
const UserAgent = "PlumblineCrawler/0.1 (+https://github.com/GautamTalksDev/Plumbline/blob/main/docs/CRAWLING.md)"

// Event types recorded for every server (including failures).
// events-v2 replaces transport_error with the mutually exclusive classes below.
const (
	EventOK           = "ok"
	EventAuthRequired = "auth_required"
	EventEndpoint404  = "endpoint_404"
	EventDNS          = "dns_failure"
	EventTLS          = "tls_failure"
	EventHTTPError    = "http_error"
	EventProtocol     = "protocol_error"
	EventMalformed    = "malformed"
	EventTimeout      = "timeout"
	EventSandbox      = "sandbox_error"
	EventRefused      = "refused"
	EventTooLarge     = "too_large"
	EventOptOut       = "opt_out"
)

// StatusToolName is the tool_name used for per-server status log entries.
const StatusToolName = "_crawl_status"

// Config controls a crawl run.
type Config struct {
	RegistryURL   string
	StatePath     string
	LogPath       string
	OptOutPath    string
	SigningKey    ed25519.PrivateKey
	Concurrency   int
	RequestDelay  time.Duration
	Sandbox       Sandbox
	Registry      RegistryClient
	Clock         func() time.Time
	SkipLiveGuard bool // tests only
}

// Summary is returned at the end of a crawl run.
type Summary struct {
	ServersSeen int             `json:"servers_seen"`
	ServersDone int             `json:"servers_done"`
	EventCounts map[string]int  `json:"event_counts"`
	ToolsLogged int             `json:"tools_logged"`
	Checkpoint  *log.Checkpoint `json:"checkpoint,omitempty"`
	Resumed     bool            `json:"resumed"`
	Duration    time.Duration   `json:"duration"`
}

// Observation is one sandboxed probe result.
type Observation struct {
	ServerID        string
	EventType       string
	Detail          string // fixed diagnostic (legacy field name; same as Diagnostic)
	Diagnostic      string
	HTTPStatus      *int
	EventsVersion   string
	ProtocolVersion string
	Tools           []json.RawMessage
}

// Run executes a crawl: enumerate → sandbox observe → merkle append → checkpoint.
func Run(ctx context.Context, cfg Config) (Summary, error) {
	if !cfg.SkipLiveGuard && os.Getenv("PLUMBLINE_CRAWL_I_UNDERSTAND") != "1" {
		return Summary{}, fmt.Errorf("crawler: refuse to start: set PLUMBLINE_CRAWL_I_UNDERSTAND=1 after reading docs/CRAWLING.md and passing containment tests")
	}
	if cfg.RegistryURL == "" {
		cfg.RegistryURL = DefaultRegistryURL
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.RequestDelay <= 0 {
		cfg.RequestDelay = 250 * time.Millisecond
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.StatePath == "" || cfg.LogPath == "" {
		return Summary{}, fmt.Errorf("crawler: StatePath and LogPath required")
	}
	if cfg.Registry == nil {
		cfg.Registry = NewHTTPRegistry(cfg.RegistryURL, UserAgent)
	}
	if cfg.Sandbox == nil {
		sb, err := DefaultSandbox()
		if err != nil {
			return Summary{}, err
		}
		cfg.Sandbox = sb
	}

	start := cfg.Clock()
	st, err := LoadState(cfg.StatePath)
	if err != nil {
		return Summary{}, err
	}
	resumed := st.SnapshotCursor() != "" || st.Pending() || len(st.Completed) > 0

	optOut, err := LoadOptOut(cfg.OptOutPath)
	if err != nil {
		return Summary{}, err
	}

	lg, err := log.Open(cfg.LogPath)
	if err != nil {
		return Summary{}, err
	}
	defer lg.Close()

	sum := Summary{EventCounts: map[string]int{}, Resumed: resumed}
	jobs := make(chan RegistryServer)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		runErr error
	)
	setErr := func(e error) {
		mu.Lock()
		defer mu.Unlock()
		if runErr == nil {
			runErr = e
		}
	}

	worker := func() {
		defer wg.Done()
		for srv := range jobs {
			if ctx.Err() != nil {
				continue
			}
			var obs Observation
			if optOut.IsOptedOut(srv.Name) || srv.OptOutMeta {
				obs = Observation{
					ServerID:      srv.Name,
					EventType:     EventOptOut,
					Detail:        DiagOptOut,
					Diagnostic:    DiagOptOut,
					EventsVersion: EventsVersionV2,
				}
			} else {
				obs = cfg.Sandbox.Observe(ctx, TargetFromRegistry(srv))
			}
			if obs.EventsVersion == "" {
				obs.EventsVersion = EventsVersionV2
			}
			if obs.Diagnostic == "" && obs.Detail != "" {
				obs.Diagnostic = obs.Detail
			}

			mu.Lock()
			n, err := recordObservation(lg, obs)
			if err != nil {
				mu.Unlock()
				setErr(err)
				continue
			}
			sum.EventCounts[obs.EventType]++
			sum.ToolsLogged += n
			sum.ServersDone++
			sum.ServersSeen++
			mu.Unlock()

			if err := st.MarkCompleted(cfg.StatePath, srv.Name); err != nil {
				setErr(err)
			}
		}
	}

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go worker()
	}

	// Seed: resume in-flight, then drain queue, then paginate registry.
	send := func(srv RegistryServer) bool {
		select {
		case <-ctx.Done():
			return false
		case jobs <- srv:
			return true
		}
	}
	for _, srv := range st.DrainInFlight() {
		if !send(srv) {
			break
		}
	}
	for ctx.Err() == nil {
		mu.Lock()
		fatal := runErr
		mu.Unlock()
		if fatal != nil {
			break
		}

		srv, ok, err := st.PopQueue(cfg.StatePath)
		if err != nil {
			setErr(err)
			break
		}
		if ok {
			if !send(srv) {
				break
			}
			continue
		}

		time.Sleep(cfg.RequestDelay)
		page, next, err := cfg.Registry.ListServers(ctx, st.SnapshotCursor(), 50)
		if err != nil {
			setErr(err)
			break
		}
		if len(page) == 0 && next == "" {
			break
		}
		if err := st.EnqueuePage(cfg.StatePath, page, next); err != nil {
			setErr(err)
			break
		}
		if !st.HasQueue() && next == "" {
			break
		}
	}
	close(jobs)
	wg.Wait()

	if len(cfg.SigningKey) == ed25519.PrivateKeySize {
		cp, err := lg.SignCheckpoint(cfg.SigningKey)
		if err != nil {
			sum.Duration = cfg.Clock().Sub(start)
			return sum, err
		}
		sum.Checkpoint = &cp
	}
	sum.Duration = cfg.Clock().Sub(start)

	mu.Lock()
	defer mu.Unlock()
	if runErr != nil {
		return sum, runErr
	}
	if ctx.Err() != nil {
		return sum, ctx.Err()
	}
	return sum, nil
}

func recordObservation(lg *log.Log, obs Observation) (toolsLogged int, err error) {
	exists, err := lg.HasTool(obs.ServerID, StatusToolName)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, nil
	}

	statusCanon, statusHash, err := eventCanonical(obs)
	if err != nil {
		return 0, err
	}
	_, err = lg.Append(log.Entry{
		ServerID:        obs.ServerID,
		ToolName:        StatusToolName,
		Canonical:       statusCanon,
		Hash:            statusHash,
		ProtocolVersion: obs.ProtocolVersion,
		CanonVersion:    log.CanonVersionV1,
	})
	if err != nil {
		return 0, err
	}
	if obs.EventType != EventOK {
		return 0, nil
	}
	for _, raw := range obs.Tools {
		name := toolName(raw)
		if ok, err := lg.HasTool(obs.ServerID, name); err != nil {
			return toolsLogged, err
		} else if ok {
			continue
		}
		h, err := canonical.Hash(raw)
		if err != nil {
			continue
		}
		c, err := canonical.Canonicalize(raw)
		if err != nil {
			continue
		}
		_, err = lg.Append(log.Entry{
			ServerID:        obs.ServerID,
			ToolName:        name,
			Canonical:       c,
			Hash:            h,
			ProtocolVersion: obs.ProtocolVersion,
			CanonVersion:    log.CanonVersionV1,
		})
		if err != nil {
			return toolsLogged, err
		}
		toolsLogged++
	}
	return toolsLogged, nil
}

func eventCanonical(obs Observation) ([]byte, string, error) {
	diag := obs.Diagnostic
	if diag == "" {
		diag = obs.Detail
	}
	payload, err := json.Marshal(struct {
		Event         string `json:"event"`
		Server        string `json:"server_id"`
		Diagnostic    string `json:"diagnostic,omitempty"`
		HTTPStatus    *int   `json:"http_status"`
		EventsVersion string `json:"events_version"`
		ToolCount     int    `json:"tool_count,omitempty"`
	}{
		Event:         obs.EventType,
		Server:        obs.ServerID,
		Diagnostic:    diag,
		HTTPStatus:    obs.HTTPStatus,
		EventsVersion: obs.EventsVersion,
		ToolCount:     len(obs.Tools),
	})
	if err != nil {
		return nil, "", err
	}
	wrapped, err := json.Marshal(map[string]any{
		"name":        StatusToolName,
		"description": string(payload),
		"_meta":       json.RawMessage(payload),
	})
	if err != nil {
		return nil, "", err
	}
	c, err := canonical.Canonicalize(wrapped)
	if err != nil {
		return nil, "", err
	}
	h, err := canonical.Hash(wrapped)
	return c, h, err
}

func toolName(raw json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &v)
	if v.Name == "" {
		return "<unnamed>"
	}
	return v.Name
}
