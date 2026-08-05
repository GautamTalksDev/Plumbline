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
	EventVersionPoll  = "version_poll"
	EventUnpollable   = "unpollable"
	EventFetchError   = "fetch_error"
	EventDeparted     = "departed"
)

// StatusToolName is the legacy per-server status tool name (pre-run-scoped).
const StatusToolName = "_crawl_status"

// PollToolPrefix and SpawnToolPrefix are run-scoped Merkle tool name prefixes.
const (
	PollToolPrefix  = "_version_poll/"
	SpawnToolPrefix = "_crawl_status/"
)

func pollToolName(runID string) string  { return PollToolPrefix + runID }
func spawnToolName(runID string) string { return SpawnToolPrefix + runID }

// Config controls a crawl run.
type Config struct {
	RegistryURL      string
	StatePath        string
	LogPath          string
	OptOutPath       string
	SummaryPath      string
	CohortPath       string
	SigningKey       ed25519.PrivateKey
	Concurrency      int // tier-1 default 8
	Tier2Concurrency int
	RequestDelay     time.Duration
	Sandbox          Sandbox
	Registry         RegistryClient
	PackageMeta      PackageMetaClient
	PackageFetch     PackageFetcher
	Clock            func() time.Time
	SkipLiveGuard    bool
	IncludeRemotes   bool
	FrozenCohort     map[string]bool
}

// Observation is one probe or poll result.
type Observation struct {
	ServerID        string
	EventType       string
	Detail          string
	Diagnostic      string
	HTTPStatus      *int
	EventsVersion   string
	ProtocolVersion string
	Tools           []json.RawMessage

	// Tier-1 version poll fields (optional).
	Identifier    string
	RegistryType  string
	PinnedVersion string
	LatestVersion string
	VersionDiffer bool
	PublishedAt   string
	PolledAt      string
	CohortMember  bool
	SpawnReason   string
}

// Run executes a two-tier crawl: version poll → targeted spawn → checkpoint.
func Run(ctx context.Context, cfg Config) (Summary, error) {
	if !cfg.SkipLiveGuard && os.Getenv("PLUMBLINE_CRAWL_I_UNDERSTAND") != "1" {
		return Summary{}, fmt.Errorf("crawler: refuse to start: set PLUMBLINE_CRAWL_I_UNDERSTAND=1 after reading docs/CRAWLING.md and passing containment tests")
	}
	if cfg.RegistryURL == "" {
		cfg.RegistryURL = DefaultRegistryURL
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.Tier2Concurrency <= 0 {
		cfg.Tier2Concurrency = 4
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
	if cfg.PackageMeta == nil {
		cfg.PackageMeta = &HTTPPackageMeta{UserAgent: UserAgent}
	}
	if cfg.PackageFetch == nil {
		cfg.PackageFetch = &HTTPPackageFetcher{UserAgent: UserAgent}
	}
	if cfg.IncludeRemotes && cfg.Sandbox == nil {
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

	resumed := !st.NeedsNewRun()
	if st.NeedsNewRun() {
		runID := cfg.Clock().UTC().Format("2006-01-02")
		if err := st.BeginRun(cfg.StatePath, runID); err != nil {
			return Summary{}, err
		}
		resumed = false
	}

	cohort := cfg.FrozenCohort
	if cohort == nil {
		cohort, err = LoadFrozenCohort(cfg.CohortPath)
		if err != nil {
			return Summary{}, err
		}
	}

	optOut, err := LoadOptOut(cfg.OptOutPath)
	if err != nil {
		return Summary{}, err
	}

	lg, err := log.Open(cfg.LogPath)
	if err != nil {
		return Summary{}, err
	}
	defer lg.Close()

	sum := Summary{
		RunID:       st.RunID,
		EventCounts: map[string]int{},
		Resumed:     resumed,
	}

	var runErr error
	finish := func() (Summary, error) {
		snap := st.Snapshot()
		sum.RunID = snap.RunID
		sum.Tier1Complete = snap.Tier1Complete
		sum.CohortComplete = snap.CohortComplete
		sum.RemotesExcluded = snap.RemotesExcluded
		sum.Complete = snap.RunComplete
		sum.CountsTowardSix = snap.RunComplete
		sum.Duration = cfg.Clock().Sub(start)
		sum.DurationMS = sum.Duration.Milliseconds()
		if runErr != nil {
			sum.Error = runErr.Error()
		}
		if cfg.SummaryPath != "" {
			_ = WriteSummaryAtomic(cfg.SummaryPath, sum)
		}
		if runErr != nil {
			return sum, runErr
		}
		if ctx.Err() != nil {
			return sum, ctx.Err()
		}
		return sum, nil
	}

	if !st.Snapshot().Tier1Complete {
		if err := runTier1(ctx, cfg, st, lg, optOut, &sum); err != nil {
			runErr = err
			return finish()
		}
		if ctx.Err() != nil {
			runErr = ctx.Err()
			return finish()
		}
		// Build tier-2 queue once tier-1 queue/pages drained.
		if st.Pending() {
			return finish() // incomplete
		}
		jobs, cohortTodo := buildTier2Jobs(st, cohort)
		if err := st.SetTier2Queue(cfg.StatePath, jobs, cohortTodo); err != nil {
			runErr = err
			return finish()
		}
	}

	if cfg.Sandbox == nil && (st.Tier2Pending() || len(st.DrainTier2InFlight()) > 0) {
		sb, err := DefaultSandbox()
		if err != nil {
			runErr = err
			return finish()
		}
		cfg.Sandbox = sb
	}

	if !st.Snapshot().RunComplete {
		if err := runTier2(ctx, cfg, st, lg, &sum); err != nil {
			runErr = err
			return finish()
		}
	}

	snap := st.Snapshot()
	complete := snap.Tier1Complete && snap.CohortComplete && !st.Tier2Pending()
	if complete {
		if len(cfg.SigningKey) == ed25519.PrivateKeySize {
			cp, err := lg.SignCheckpoint(cfg.SigningKey)
			if err != nil {
				runErr = err
				return finish()
			}
			sum.Checkpoint = &cp
		}
		if err := st.MarkRunComplete(cfg.StatePath); err != nil {
			runErr = err
			return finish()
		}
	}
	return finish()
}

func runTier1(ctx context.Context, cfg Config, st *State, lg *log.Log, optOut *OptOutList, sum *Summary) error {
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

	tool := pollToolName(st.RunID)
	worker := func() {
		defer wg.Done()
		for srv := range jobs {
			if ctx.Err() != nil {
				continue
			}
			var obs Observation
			if optOut.IsOptedOut(srv.Name) || srv.OptOutMeta {
				obs = Observation{
					ServerID: srv.Name, EventType: EventOptOut,
					Diagnostic: DiagOptOut, Detail: DiagOptOut, EventsVersion: EventsVersionV2,
				}
			} else if cfg.IncludeRemotes && RemoteOnly(srv) {
				obs = cfg.Sandbox.Observe(ctx, TargetFromRegistry(srv))
			} else {
				pr := PollServer(ctx, cfg.PackageMeta, srv, cfg.Clock())
				obs = observationFromPoll(pr)
			}
			normalizeObs(&obs)

			mu.Lock()
			n, err := recordObservation(lg, obs, tool)
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

			snap := pollSnapshotFromObs(obs)
			_ = st.RecordPoll(cfg.StatePath, snap, srv.Name)
			if err := st.MarkCompleted(cfg.StatePath, srv.Name); err != nil {
				setErr(err)
			}
		}
	}

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go worker()
	}

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
		if err := st.EnqueuePage(cfg.StatePath, page, next, cfg.IncludeRemotes); err != nil {
			setErr(err)
			break
		}
		if !st.HasQueue() && next == "" {
			break
		}
	}
	close(jobs)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return runErr
}

func runTier2(ctx context.Context, cfg Config, st *State, lg *log.Log, sum *Summary) error {
	if cfg.Sandbox == nil {
		return fmt.Errorf("crawler: sandbox required for tier 2")
	}
	jobs := make(chan Tier2Job)
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
	tool := spawnToolName(st.RunID)
	worker := func() {
		defer wg.Done()
		for job := range jobs {
			if ctx.Err() != nil {
				continue
			}
			var obs Observation
			switch job.Reason {
			case "cohort_departed":
				obs = Observation{
					ServerID: job.Server.Name, EventType: EventDeparted,
					Diagnostic: DiagDeparted, Detail: DiagDeparted,
					EventsVersion: EventsVersionV2, CohortMember: true,
				}
			default:
				obs = observePackageFetchThenSpawn(ctx, cfg.Sandbox, cfg.PackageFetch, job.Server, job.Latest)
				obs.CohortMember = job.CohortMember
				obs.SpawnReason = job.Reason
				obs.LatestVersion = job.Latest
			}
			normalizeObs(&obs)
			mu.Lock()
			n, err := recordObservation(lg, obs, tool)
			if err != nil {
				mu.Unlock()
				setErr(err)
				continue
			}
			sum.EventCounts[obs.EventType]++
			sum.ToolsLogged += n
			sum.Tier2Spawned++
			mu.Unlock()
			if err := st.MarkTier2Completed(cfg.StatePath, job.Server.Name, job.CohortMember); err != nil {
				setErr(err)
			}
		}
	}
	n := cfg.Tier2Concurrency
	for i := 0; i < n; i++ {
		wg.Add(1)
		go worker()
	}
	send := func(job Tier2Job) bool {
		select {
		case <-ctx.Done():
			return false
		case jobs <- job:
			return true
		}
	}
	for _, job := range st.DrainTier2InFlight() {
		if !send(job) {
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
		job, ok, err := st.PopTier2(cfg.StatePath)
		if err != nil {
			setErr(err)
			break
		}
		if !ok {
			break
		}
		if !send(job) {
			break
		}
	}
	close(jobs)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return runErr
}

func buildTier2Jobs(st *State, frozenCohort map[string]bool) ([]Tier2Job, map[string]bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	prev := st.LastPoll
	this := st.ThisPoll
	byID := map[string]RegistryServer{}
	for _, s := range st.Queue {
		byID[s.Name] = s
	}
	// Completed servers aren't in queue; reconstruct minimal server from ThisPoll + name.
	for id, snap := range this {
		if _, ok := byID[id]; ok {
			continue
		}
		byID[id] = RegistryServer{
			Name: id,
			Packages: []Package{{
				RegistryType: snap.RegistryType,
				Identifier:   snap.Identifier,
				Version:      snap.PinnedVersion,
			}},
		}
	}

	cohortTodo := map[string]bool{}
	if frozenCohort != nil {
		for id := range frozenCohort {
			cohortTodo[id] = true
		}
	} else {
		for id := range this {
			if InControlCohort(id) {
				cohortTodo[id] = true
			}
		}
		// Also keep prior cohort members that disappeared from ThisPoll.
		for id := range prev {
			if InControlCohort(id) {
				cohortTodo[id] = true
			}
		}
	}

	seen := map[string]bool{}
	var jobs []Tier2Job

	add := func(job Tier2Job) {
		id := job.Server.Name
		if seen[id] {
			// Prefer keeping cohort flag if duplicate.
			for i := range jobs {
				if jobs[i].Server.Name == id {
					jobs[i].CohortMember = jobs[i].CohortMember || job.CohortMember
					if job.Reason == "cohort" || job.Reason == "cohort_departed" {
						jobs[i].Reason = job.Reason
					}
				}
			}
			return
		}
		seen[id] = true
		jobs = append(jobs, job)
	}

	for id, snap := range this {
		prevSnap, had := prev[id]
		changed := had && prevSnap.LatestVersion != "" && snap.LatestVersion != "" && prevSnap.LatestVersion != snap.LatestVersion
		if changed {
			srv := byID[id]
			add(Tier2Job{Server: srv, Latest: snap.LatestVersion, Reason: "version_change"})
		}
	}

	for id := range cohortTodo {
		srv, inReg := byID[id]
		if !inReg {
			// Departed: still in frozen cohort / algorithmic set but not polled this run.
			add(Tier2Job{
				Server:       RegistryServer{Name: id},
				Reason:       "cohort_departed",
				CohortMember: true,
			})
			continue
		}
		latest := ""
		if snap, ok := this[id]; ok {
			latest = snap.LatestVersion
		}
		add(Tier2Job{Server: srv, Latest: latest, Reason: "cohort", CohortMember: true})
	}

	return jobs, cohortTodo
}

func observationFromPoll(pr PollResult) Observation {
	pub := ""
	if !pr.PublishedAt.IsZero() {
		pub = pr.PublishedAt.UTC().Format(time.RFC3339)
	}
	diag := pr.Diagnostic
	if pr.EventType == EventVersionPoll && diag == DiagOK {
		diag = DiagVersionPoll
	}
	return Observation{
		ServerID:      pr.ServerID,
		EventType:     pr.EventType,
		Diagnostic:    diag,
		Detail:        diag,
		HTTPStatus:    pr.HTTPStatus,
		EventsVersion: EventsVersionV2,
		Identifier:    pr.Identifier,
		RegistryType:  pr.RegistryType,
		PinnedVersion: pr.PinnedVersion,
		LatestVersion: pr.LatestVersion,
		VersionDiffer: pr.Differ,
		PublishedAt:   pub,
		PolledAt:      pr.PolledAt.UTC().Format(time.RFC3339),
	}
}

func pollSnapshotFromObs(obs Observation) PollSnapshot {
	return PollSnapshot{
		Identifier:    obs.Identifier,
		RegistryType:  obs.RegistryType,
		PinnedVersion: obs.PinnedVersion,
		LatestVersion: obs.LatestVersion,
		Differ:        obs.VersionDiffer,
		PublishedAt:   obs.PublishedAt,
		PolledAt:      obs.PolledAt,
		Event:         obs.EventType,
	}
}

func normalizeObs(obs *Observation) {
	if obs.EventsVersion == "" {
		obs.EventsVersion = EventsVersionV2
	}
	if obs.Diagnostic == "" && obs.Detail != "" {
		obs.Diagnostic = obs.Detail
	}
}

func recordObservation(lg *log.Log, obs Observation, toolName string) (toolsLogged int, err error) {
	if toolName == "" {
		toolName = StatusToolName
	}
	exists, err := lg.HasTool(obs.ServerID, toolName)
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
		ToolName:        toolName,
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
		name := toolNameFromRaw(raw)
		scoped := name
		// Keep tool hashes under plain tool names; uniqueness is per server.
		if ok, err := lg.HasTool(obs.ServerID, scoped); err != nil {
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
			ToolName:        scoped,
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
		Event          string `json:"event"`
		Server         string `json:"server_id"`
		Diagnostic     string `json:"diagnostic,omitempty"`
		HTTPStatus     *int   `json:"http_status"`
		EventsVersion  string `json:"events_version"`
		ToolCount      int    `json:"tool_count,omitempty"`
		Identifier     string `json:"identifier,omitempty"`
		RegistryType   string `json:"registry_type,omitempty"`
		PinnedVersion  string `json:"pinned_version,omitempty"`
		LatestVersion  string `json:"latest_version,omitempty"`
		VersionDiffer  bool   `json:"version_differ,omitempty"`
		PublishedAt    string `json:"published_at,omitempty"`
		PolledAt       string `json:"polled_at,omitempty"`
		CohortMember   bool   `json:"cohort_member,omitempty"`
		SpawnReason    string `json:"spawn_reason,omitempty"`
	}{
		Event:         obs.EventType,
		Server:        obs.ServerID,
		Diagnostic:    diag,
		HTTPStatus:    obs.HTTPStatus,
		EventsVersion: obs.EventsVersion,
		ToolCount:     len(obs.Tools),
		Identifier:    obs.Identifier,
		RegistryType:  obs.RegistryType,
		PinnedVersion: obs.PinnedVersion,
		LatestVersion: obs.LatestVersion,
		VersionDiffer: obs.VersionDiffer,
		PublishedAt:   obs.PublishedAt,
		PolledAt:      obs.PolledAt,
		CohortMember:  obs.CohortMember,
		SpawnReason:   obs.SpawnReason,
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

func toolNameFromRaw(raw json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &v)
	if v.Name == "" {
		return "<unnamed>"
	}
	return v.Name
}
