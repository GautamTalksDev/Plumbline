package crawler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Summary is returned at the end of a crawl run and written to summary.json.
type Summary struct {
	RunID           string         `json:"run_id"`
	Complete        bool           `json:"complete"`
	CountsTowardSix bool           `json:"counts_toward_six_week_series"`
	DryRun          bool           `json:"dry_run,omitempty"`
	Tier1Complete   bool           `json:"tier1_complete"`
	CohortComplete  bool           `json:"cohort_complete"`
	ServersSeen     int            `json:"servers_seen"`
	ServersDone     int            `json:"servers_done"`
	RemotesExcluded int            `json:"remotes_excluded"`
	Tier2Spawned    int            `json:"tier2_spawned"`
	EventCounts     map[string]int `json:"event_counts"`
	ToolsLogged     int            `json:"tools_logged"`

	// Cohort observability (gate 5 / G5a–G5d).
	CohortSpawned         int     `json:"cohort_spawned"`
	OKCount               int     `json:"ok_count"` // cohort ok
	CredentialsRequired   int     `json:"credentials_required"`
	NetworkRequired       int     `json:"network_required_at_runtime"`
	RuntimeIncompatible   int     `json:"runtime_incompatible"`
	PackageBroken         int     `json:"package_broken"`
	UsageMisconfigured    int     `json:"usage_misconfigured"`
	SilentExit            int     `json:"silent_exit"`
	ObservablePopulation  int     `json:"observable_population"`
	OKOfObservable        float64 `json:"ok_of_observable"`
	UnexplainedExecFailed int     `json:"unexplained_exec_failed"`
	UnexplainedPct        float64 `json:"unexplained_pct_of_cohort"`
	BlindObservations     int     `json:"blind_observations"` // non-ok/timeout lacking stderr and exit

	Checkpoint *log.Checkpoint `json:"checkpoint,omitempty"`
	Resumed    bool            `json:"resumed"`
	Duration   time.Duration   `json:"duration"`
	DurationMS int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
}

// CountsTowardSixWeekSeries reports whether this run may count in the kill-test series.
func (s Summary) CountsTowardSixWeekSeries() bool {
	return s.Complete && s.CountsTowardSix
}

// WriteSummaryAtomic writes summary via temp file + fsync + rename.
func WriteSummaryAtomic(path string, sum Summary) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("crawler: summary rename: %w", err)
	}
	return nil
}
