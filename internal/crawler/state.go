package crawler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State is persisted crawl progress for crash-safe resume.
type State struct {
	mu sync.Mutex `json:"-"`

	PageCursor string                    `json:"page_cursor"`
	Queue      []RegistryServer          `json:"queue"`
	InFlight   map[string]RegistryServer `json:"in_flight"`
	Completed  map[string]bool           `json:"completed"`
}

// LoadState reads crawl state from path, or returns empty state.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				Completed: map[string]bool{},
				InFlight:  map[string]RegistryServer{},
			}, nil
		}
		return nil, fmt.Errorf("crawler: load state: %w", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("crawler: parse state: %w", err)
	}
	if st.Completed == nil {
		st.Completed = map[string]bool{}
	}
	if st.InFlight == nil {
		st.InFlight = map[string]RegistryServer{}
	}
	return &st, nil
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
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DrainInFlight returns and clears the in-flight set for resume seeding.
// Entries remain marked in-flight on disk until MarkCompleted; we re-persist
// the same map so a crash mid-run still resumes them.
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

// MarkCompleted records a finished server and drops it from InFlight.
func (st *State) MarkCompleted(path, name string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.Completed == nil {
		st.Completed = map[string]bool{}
	}
	st.Completed[name] = true
	delete(st.InFlight, name)
	return saveStateLocked(path, st)
}

// EnqueuePage appends servers not yet completed/in-flight and advances the cursor.
func (st *State) EnqueuePage(path string, page []RegistryServer, nextCursor string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range page {
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

// Pending reports whether work remains in queue or in-flight.
func (st *State) Pending() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.Queue) > 0 || len(st.InFlight) > 0
}

// HasQueue reports whether the queue is non-empty.
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
