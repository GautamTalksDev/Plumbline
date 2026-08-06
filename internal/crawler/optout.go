package crawler

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// OptOutList is a set of server names that must not be contacted.
type OptOutList struct {
	names map[string]bool
}

// LoadOptOut loads a local opt-out file (one server name per line).
// Missing file is treated as an empty list.
func LoadOptOut(path string) (*OptOutList, error) {
	out := &OptOutList{names: map[string]bool{}}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("crawler: opt-out: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out.names[line] = true
	}
	return out, sc.Err()
}

// IsOptedOut reports whether name is on the opt-out list.
func (o *OptOutList) IsOptedOut(name string) bool {
	if o == nil {
		return false
	}
	return o.names[name]
}
