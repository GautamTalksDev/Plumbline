package crawler

import (
	"encoding/json"
	"testing"
)

func TestExitCodeZeroSurvivesCanonical(t *testing.T) {
	obs := Observation{
		ServerID:      "test/exit0",
		EventType:     EventExecFailed,
		Diagnostic:    DiagExecFailed,
		EventsVersion: EventsVersionV2,
		ExitCode:      0,
		ExitCodeSet:   true,
	}
	c, _, err := eventCanonical(obs)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical bytes include the status tool wrapper; parse _meta via description.
	var wrap struct {
		Meta json.RawMessage `json:"_meta"`
	}
	// eventCanonical returns plumbline-canon-v1\n{...}
	body := c
	if idx := indexByte(body, '\n'); idx >= 0 {
		body = body[idx+1:]
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		t.Fatalf("unmarshal wrap: %v\n%s", err, body)
	}
	var meta map[string]any
	if err := json.Unmarshal(wrap.Meta, &meta); err != nil {
		t.Fatal(err)
	}
	v, ok := meta["exit_code"]
	if !ok {
		t.Fatalf("exit_code missing from canonical meta: %v", meta)
	}
	if v != float64(0) {
		t.Fatalf("exit_code=%v want 0", v)
	}
}

func TestObservationBlindInvariant(t *testing.T) {
	cases := []struct {
		name  string
		obs   Observation
		blind bool
	}{
		{
			name: "exec_failed with exit 0 is not blind",
			obs: Observation{
				EventType:   EventExecFailed,
				ExitCode:    0,
				ExitCodeSet: true,
			},
			blind: false,
		},
		{
			name: "exec_failed with stderr is not blind",
			obs: Observation{
				EventType:     EventExecFailed,
				StderrPreview: "boom",
			},
			blind: false,
		},
		{
			name: "exec_failed with neither is blind",
			obs: Observation{
				EventType: EventExecFailed,
			},
			blind: true,
		},
		{
			name: "timeout may lack both",
			obs: Observation{
				EventType: EventTimeout,
			},
			blind: false,
		},
		{
			name: "containment may lack both",
			obs: Observation{
				EventType: EventContainmentFailed,
			},
			blind: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := observationBlind(tc.obs); got != tc.blind {
				t.Fatalf("observationBlind=%v want %v", got, tc.blind)
			}
		})
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
