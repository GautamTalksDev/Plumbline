//go:build !linux

package crawler

import (
	"context"
	"fmt"
)

// NewDockerSandbox is unavailable off Linux.
func NewDockerSandbox(engine string) (*DockerSandbox, error) {
	return nil, fmt.Errorf("crawler: Docker sandbox is Linux-only")
}

// DockerSandbox placeholder type for non-linux builds.
type DockerSandbox struct{}

func (d *DockerSandbox) Observe(ctx context.Context, t Target) Observation {
	return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
}

// NewUnshareSandbox is unavailable off Linux.
func NewUnshareSandbox() (*UnshareSandbox, error) {
	return nil, fmt.Errorf("crawler: unshare sandbox is Linux-only")
}

type UnshareSandbox struct{}

func (u *UnshareSandbox) Observe(ctx context.Context, t Target) Observation {
	return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
}

// DockerRunArgs documents expected flags for reviewers on all platforms.
func DockerRunArgs(t Target) []string {
	return []string{
		"run", "--rm",
		"--user", "65534:65534",
		"--read-only",
		"--memory", "512m",
		"--cpus", "0.5",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--network", "none",
	}
}
