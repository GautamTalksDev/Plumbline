// Package check runs `plumb check`: observe configured MCP tools vs baseline.
package check

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/baseline"
	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/discover"
	"github.com/GautamTalksDev/Plumbline/internal/jsondiff"
	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// Exit codes for plumb check / accept.
const (
	ExitClean = 0
	ExitDrift = 1
	ExitError = 2
)

// ToolSource is the subset of mcpclient.Session used by check.
type ToolSource interface {
	ListTools(ctx context.Context) (mcpclient.ToolList, error)
	Close() error
}

// ToolObservation is one live tool hash.
type ToolObservation struct {
	Server  string          `json:"server"`
	Tool    string          `json:"tool"`
	Hash    string          `json:"hash"`
	Raw     json.RawMessage `json:"tool_json,omitempty"`
	OldHash string          `json:"old_hash,omitempty"`
	Diff    string          `json:"diff,omitempty"`
	Status  string          `json:"status"` // clean|drift|new|error
	Error   string          `json:"error,omitempty"`
}

// Result is the full check outcome.
type Result struct {
	BaselineCreated bool              `json:"baseline_created"`
	BaselinePath    string            `json:"baseline_path"`
	ScannedConfigs  []string          `json:"scanned_configs"`
	ServersFound    int               `json:"servers_found"`
	Observations    []ToolObservation `json:"observations"`
	DriftCount      int               `json:"drift_count"`
	ErrorCount      int               `json:"error_count"`
	CleanCount      int               `json:"clean_count"`
	NewCount        int               `json:"new_count"`
	DurationMS      int64             `json:"duration_ms"`
	ExitCode        int               `json:"exit_code"`
}

// Options controls check behavior.
type Options struct {
	// Connect opens a tool source for a server. Defaults to mcpclient.Connect.
	Connect func(ctx context.Context, spec mcpclient.ServerSpec) (ToolSource, error)
}

func defaultConnect(ctx context.Context, spec mcpclient.ServerSpec) (ToolSource, error) {
	return mcpclient.Connect(ctx, spec)
}

// Run discovers servers, lists tools, compares to baseline.
func Run(ctx context.Context, opt Options) (Result, error) {
	start := time.Now()
	if opt.Connect == nil {
		opt.Connect = defaultConnect
	}

	basePath, err := baseline.Path()
	if err != nil {
		return Result{ExitCode: ExitError}, err
	}

	servers, scanned, err := discover.Find()
	if err != nil {
		return Result{ExitCode: ExitError, ScannedConfigs: scanned, BaselinePath: basePath}, err
	}

	bl, existed, err := baseline.Load()
	if err != nil {
		return Result{ExitCode: ExitError, ScannedConfigs: scanned, BaselinePath: basePath}, err
	}

	res := Result{
		BaselinePath:   basePath,
		ScannedConfigs: scanned,
		ServersFound:   len(servers),
	}

	creating := !existed
	pendingServers := map[string]map[string]baseline.ToolRecord{}

	for _, srv := range servers {
		obs, tools, opErr := observeServer(ctx, opt, srv)
		if opErr != nil {
			res.Observations = append(res.Observations, ToolObservation{
				Server: srv.Name,
				Status: "error",
				Error:  opErr.Error(),
			})
			continue
		}
		res.Observations = append(res.Observations, obs...)
		if creating {
			pendingServers[srv.Name] = tools
			startIdx := len(res.Observations) - len(obs)
			for i := range obs {
				res.Observations[startIdx+i].Status = "new"
			}
			continue
		}

		startIdx := len(res.Observations) - len(obs)
		for i := range obs {
			o := &res.Observations[startIdx+i]
			rec, ok := bl.Get(srv.Name, o.Tool)
			if !ok {
				o.Status = "new"
				continue
			}
			o.OldHash = rec.Hash
			if rec.Hash == o.Hash {
				o.Status = "clean"
				o.Raw = nil
				continue
			}
			o.Status = "drift"
			o.Diff = jsondiff.Diff(rec.Tool, o.Raw)
		}
	}

	for _, o := range res.Observations {
		switch o.Status {
		case "clean":
			res.CleanCount++
		case "drift":
			res.DriftCount++
		case "new":
			res.NewCount++
		case "error":
			res.ErrorCount++
		}
	}

	if creating {
		for name, tools := range pendingServers {
			bl.ReplaceServer(name, tools)
		}
		if err := baseline.Save(bl); err != nil {
			res.ExitCode = ExitError
			res.DurationMS = time.Since(start).Milliseconds()
			return res, err
		}
		res.BaselineCreated = true
		if res.ErrorCount > 0 {
			res.ExitCode = ExitError
		} else {
			res.ExitCode = ExitClean
		}
	} else {
		switch {
		case res.ErrorCount > 0:
			res.ExitCode = ExitError
		case res.DriftCount > 0:
			res.ExitCode = ExitDrift
		default:
			res.ExitCode = ExitClean
		}
	}

	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

func observeServer(ctx context.Context, opt Options, srv discover.Server) ([]ToolObservation, map[string]baseline.ToolRecord, error) {
	sess, err := opt.Connect(ctx, srv.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	defer sess.Close()

	list, err := sess.ListTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}

	tools := map[string]baseline.ToolRecord{}
	var obs []ToolObservation
	for _, raw := range list.Tools {
		name := toolName(raw)
		if name == "" {
			name = "<unnamed>"
		}
		h, err := canonical.Hash(raw)
		if err != nil {
			obs = append(obs, ToolObservation{
				Server: srv.Name,
				Tool:   name,
				Status: "error",
				Error:  err.Error(),
				Raw:    raw,
			})
			continue
		}
		copied := append(json.RawMessage(nil), raw...)
		tools[name] = baseline.ToolRecord{Hash: h, Tool: copied}
		obs = append(obs, ToolObservation{
			Server: srv.Name,
			Tool:   name,
			Hash:   h,
			Raw:    copied,
		})
	}
	return obs, tools, nil
}

func toolName(raw json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Name
}

// Accept updates the baseline for server/tool from a fresh observation.
func Accept(ctx context.Context, server, tool string, opt Options) error {
	if opt.Connect == nil {
		opt.Connect = defaultConnect
	}
	servers, _, err := discover.Find()
	if err != nil {
		return err
	}
	var target *discover.Server
	for i := range servers {
		if servers[i].Name == server {
			target = &servers[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("server %q not found in local MCP configs", server)
	}

	_, tools, err := observeServer(ctx, opt, *target)
	if err != nil {
		return err
	}
	rec, ok := tools[tool]
	if !ok {
		return fmt.Errorf("tool %q not found on server %q", tool, server)
	}

	bl, _, err := baseline.Load()
	if err != nil {
		return err
	}
	bl.Set(server, tool, rec)
	return baseline.Save(bl)
}
