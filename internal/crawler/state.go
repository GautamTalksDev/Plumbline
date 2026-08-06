package crawler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Phase names persisted in crawl state.
const (
	PhaseTier1    = "tier1"
	PhaseTier2    = "tier2"
	PhaseComplete = "complete"
)

// PollSnapshot is the last known upstream version poll for a server.
type PollSnapshot struct {
	Identifier    string   `json:"identifier"`
	RegistryType  string   `json:"registry_type"`
	PinnedVersion string   `json:"pinned_version"`
	LatestVersion string   `json:"latest_version"`
	Differ        bool     `json:"differ"`
	PublishedAt   string   `json:"published_at,omitempty"`
	PolledAt      string   `json:"polled_at"`
	Event         string   `json:"event"`
	PackageArgs   []string `json:"package_args,omitempty"`
	RuntimeArgs   []string `json:"runtime_args,omitempty"`
}

// Tier2Job is one spawn target for tier 2.
type Tier2Job struct {
	Server       RegistryServer `json:"server"`
	Latest       string         `json:"latest"`
	Reason       string         `json:"reason"` // "version_change" | "cohort" | "cohort_departed"
	CohortMember bool           `json:"cohort_member"`
}

// State is persisted crawl progress for crash-safe resume across both tiers.
type State struct {
	mu *sync.Mutex `json:"-"`

	RunID string `json:"run_id"`
	Phase string `json:"phase"`

	// Tier 1 pagination / queue (packages population only).
	PageCursor string                    `json:"page_cursor"`
	Queue      []RegistryServer          `json:"queue"`
	InFlight   map[string]RegistryServer `json:"in_flight"`
	Completed  map[string]bool           `json:"completed"`

	RemotesExcluded int `json:"remotes_excluded"`

	// Cross-run upstream versions (survives BeginRun).
	LastPoll map[string]PollSnapshot `json:"last_poll"`
	// This run's polls (used to build tier 2 and update LastPoll).
	ThisPoll map[string]PollSnapshot `json:"this_poll"`

	// Tier 2.
	Tier2Queue     []Tier2Job          `json:"tier2_queue"`
	Tier2InFlight  map[string]Tier2Job `json:"tier2_in_flight"`
	Tier2Completed map[string]bool     `json:"tier2_completed"`
	CohortTodo     map[string]bool     `json:"cohort_todo"`

	Tier1Complete  bool `json:"tier1_complete"`
	CohortComplete bool `json:"cohort_complete"`
	RunComplete    bool `json:"run_complete"`
}

// LoadState reads crawl state from path, or returns empty state.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newEmptyState(), nil
		}
		return nil, fmt.Errorf("crawler: load state: %w", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("crawler: parse state: %w", err)
	}
	st.ensureMaps()
	return &st, nil
}

func newEmptyState() *State {
	st := &State{}
	st.ensureMaps()
	return st
}

func (st *State) ensureMaps() {
	if st.mu == nil {
		st.mu = &sync.Mutex{}
	}
	if st.Completed == nil {
		st.Completed = map[string]bool{}
	}
	if st.InFlight == nil {
		st.InFlight = map[string]RegistryServer{}
	}
	if st.LastPoll == nil {
		st.LastPoll = map[string]PollSnapshot{}
	}
	if st.ThisPoll == nil {
		st.ThisPoll = map[string]PollSnapshot{}
	}
	if st.Tier2InFlight == nil {
		st.Tier2InFlight = map[string]Tier2Job{}
	}
	if st.Tier2Completed == nil {
		st.Tier2Completed = map[string]bool{}
	}
	if st.CohortTodo == nil {
		st.CohortTodo = map[string]bool{}
	}
}

// SaveState writes state atomically.
func SaveState(path string, st *State) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return saveStateLocked(path, st)
}

func saveStateLocked(path string, st *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BeginRun resets per-run progress for a new weekly run, keeping LastPoll.
func (st *State) BeginRun(path, runID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	last := st.LastPoll
	st.RunID = runID
	st.Phase = PhaseTier1
	st.PageCursor = ""
	st.Queue = nil
	st.InFlight = map[string]RegistryServer{}
	st.Completed = map[string]bool{}
	st.RemotesExcluded = 0
	st.LastPoll = last
	if st.LastPoll == nil {
		st.LastPoll = map[string]PollSnapshot{}
	}
	st.ThisPoll = map[string]PollSnapshot{}
	st.Tier2Queue = nil
	st.Tier2InFlight = map[string]Tier2Job{}
	st.Tier2Completed = map[string]bool{}
	st.CohortTodo = map[string]bool{}
	st.Tier1Complete = false
	st.CohortComplete = false
	st.RunComplete = false
	return saveStateLocked(path, st)
}

// NeedsNewRun is true when there is no active incomplete run.
func (st *State) NeedsNewRun() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.RunID == "" || st.RunComplete || st.Phase == PhaseComplete
}

// DrainInFlight returns tier-1 in-flight servers for resume seeding.
func (st *State) DrainInFlight() []RegistryServer {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]RegistryServer, 0, len(st.InFlight))
	for name, srv := range st.InFlight {
		if st.Completed[name] {
			continue
		}
		out = append(out, srv)
	}
	return out
}

// PopQueue moves the next queued server into InFlight and persists.
func (st *State) PopQueue(path string) (RegistryServer, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.Queue) == 0 {
		return RegistryServer{}, false, nil
	}
	srv := st.Queue[0]
	st.Queue = st.Queue[1:]
	st.InFlight[srv.Name] = srv
	if err := saveStateLocked(path, st); err != nil {
		return RegistryServer{}, false, err
	}
	return srv, true, nil
}

// MarkCompleted records a finished tier-1 server.
func (st *State) MarkCompleted(path, name string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Completed[name] = true
	delete(st.InFlight, name)
	return saveStateLocked(path, st)
}

// RecordPoll stores this-run poll snapshot.
func (st *State) RecordPoll(path string, snap PollSnapshot, serverID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.ThisPoll[serverID] = snap
	return saveStateLocked(path, st)
}

// EnqueuePage appends in-scope servers and advances the cursor.
// Packages population is always in scope. Remote-only servers are counted as
// excluded unless includeRemotes is true.
func (st *State) EnqueuePage(path string, page []RegistryServer, nextCursor string, includeRemotes bool) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range page {
		inScope := false
		switch {
		case HasPackages(s):
			inScope = true
		case RemoteOnly(s):
			st.RemotesExcluded++
			if includeRemotes {
				inScope = true
			}
		}
		if !inScope {
			continue
		}
		if st.Completed[s.Name] {
			continue
		}
		if _, ok := st.InFlight[s.Name]; ok {
			continue
		}
		dup := false
		for _, q := range st.Queue {
			if q.Name == s.Name {
				dup = true
				break
			}
		}
		if !dup {
			st.Queue = append(st.Queue, s)
		}
	}
	st.PageCursor = nextCursor
	return saveStateLocked(path, st)
}

// Pending reports whether tier-1 work remains.
func (st *State) Pending() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.Queue) > 0 || len(st.InFlight) > 0
}

// HasQueue reports whether the tier-1 queue is non-empty.
func (st *State) HasQueue() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.Queue) > 0
}

// SnapshotCursor returns the page cursor under lock.
func (st *State) SnapshotCursor() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.PageCursor
}

// SetTier2Queue installs the tier-2 work list and promotes ThisPoll → LastPoll.
func (st *State) SetTier2Queue(path string, jobs []Tier2Job, cohortTodo map[string]bool) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Tier2Queue = jobs
	st.CohortTodo = cohortTodo
	st.Tier1Complete = true
	st.Phase = PhaseTier2
	if len(cohortTodo) == 0 {
		st.CohortComplete = true
	}
	for id, snap := range st.ThisPoll {
		st.LastPoll[id] = snap
	}
	return saveStateLocked(path, st)
}

// DrainTier2InFlight returns tier-2 in-flight jobs.
func (st *State) DrainTier2InFlight() []Tier2Job {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Tier2Job, 0, len(st.Tier2InFlight))
	for id, job := range st.Tier2InFlight {
		if st.Tier2Completed[id] {
			continue
		}
		out = append(out, job)
	}
	return out
}

// PopTier2 moves the next tier-2 job into in-flight.
func (st *State) PopTier2(path string) (Tier2Job, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.Tier2Queue) == 0 {
		return Tier2Job{}, false, nil
	}
	job := st.Tier2Queue[0]
	st.Tier2Queue = st.Tier2Queue[1:]
	st.Tier2InFlight[job.Server.Name] = job
	if err := saveStateLocked(path, st); err != nil {
		return Tier2Job{}, false, err
	}
	return job, true, nil
}

// MarkTier2Completed records a finished tier-2 job.
func (st *State) MarkTier2Completed(path, name string, cohortMember bool) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Tier2Completed[name] = true
	delete(st.Tier2InFlight, name)
	if cohortMember {
		delete(st.CohortTodo, name)
	}
	if len(st.CohortTodo) == 0 {
		st.CohortComplete = true
	}
	return saveStateLocked(path, st)
}

// Tier2Pending reports remaining tier-2 work.
func (st *State) Tier2Pending() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.Tier2Queue) > 0 || len(st.Tier2InFlight) > 0
}

// MarkRunComplete finalizes a successful weekly run.
func (st *State) MarkRunComplete(path string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.RunComplete = true
	st.Phase = PhaseComplete
	st.CohortComplete = true
	st.Tier1Complete = true
	return saveStateLocked(path, st)
}

// Snapshot copies fields needed for summary without holding the lock long.
func (st *State) Snapshot() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	cp := *st
	cp.mu = nil
	return cp
}
