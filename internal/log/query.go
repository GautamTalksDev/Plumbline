package log

import "fmt"

// HasTool reports whether any entry exists for serverID + toolName.
// Used by the crawler to avoid duplicate status rows on crash resume.
func (l *Log) HasTool(serverID, toolName string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int
	err := l.db.QueryRow(
		`SELECT COUNT(1) FROM entries WHERE server_id = ? AND tool_name = ?`,
		serverID, toolName,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("log: has tool: %w", err)
	}
	return n > 0, nil
}

// CountTool returns how many entries exist for serverID + toolName.
func (l *Log) CountTool(serverID, toolName string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int
	err := l.db.QueryRow(
		`SELECT COUNT(1) FROM entries WHERE server_id = ? AND tool_name = ?`,
		serverID, toolName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("log: count tool: %w", err)
	}
	return n, nil
}
