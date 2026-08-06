package crawler

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// CohortVersion is the frozen study control-cohort definition string.
// Changing it defines a new cohort; do not change mid-study.
const CohortVersion = "plumbline-cohort-v1"

// CohortModulus and CohortThreshold select ~5% of the packages population
// (threshold/modulus = 500/10000).
const (
	CohortModulus   uint64 = 10000
	CohortThreshold uint64 = 500
)

// CohortEligible reports whether a server may be selected into the control
// cohort: it must have packages and an installable primary registry (npm/pypi).
// oci/mcpb cannot be spawned by the tier-2 install path and are excluded at
// generation time.
func CohortEligible(s RegistryServer) bool {
	pkg, ok := PrimaryPackage(s)
	if !ok {
		return false
	}
	return Installable(pkg.RegistryType)
}

// InControlCohort reports whether serverID is in the algorithmic control cohort.
// Selection depends only on serverID and CohortVersion — no randomness, clocks, or local state.
// Callers must still intersect with CohortEligible when building the member list.
func InControlCohort(serverID string) bool {
	sum := sha256.Sum256([]byte(CohortVersion + "|" + serverID))
	n := binary.BigEndian.Uint64(sum[:8])
	return n%CohortModulus < CohortThreshold
}

// LoadFrozenCohort reads a frozen member list (one server_id per line, # comments allowed).
// Empty path returns nil (callers then use InControlCohort against the live population).
func LoadFrozenCohort(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("crawler: cohort file: %w", err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CohortMembersFrom filters servers that belong to the control cohort.
func CohortMembersFrom(servers []string) []string {
	var out []string
	for _, id := range servers {
		if InControlCohort(id) {
			out = append(out, id)
		}
	}
	return out
}
