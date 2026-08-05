package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Pollable registry types for tier-1 version polls.
const (
	RegistryNPM  = "npm"
	RegistryPyPI = "pypi"
	RegistryOCI  = "oci"
	RegistryMCPB = "mcpb"
)

// PollResult is one tier-1 version observation.
type PollResult struct {
	ServerID      string
	Identifier    string
	RegistryType  string
	PinnedVersion string
	LatestVersion string
	Differ        bool
	PublishedAt   time.Time
	PolledAt      time.Time
	EventType     string
	Diagnostic    string
	HTTPStatus    *int
}

// PackageMetaClient fetches upstream latest versions (npm / PyPI).
type PackageMetaClient interface {
	Latest(ctx context.Context, registryType, identifier string) (version string, publishedAt time.Time, err error)
}

// HTTPPackageMeta polls the public npm and PyPI JSON APIs.
type HTTPPackageMeta struct {
	Client    *http.Client
	UserAgent string
	NPMBase   string
	PyPIBase  string
}

func (h *HTTPPackageMeta) ua() string {
	if h.UserAgent != "" {
		return h.UserAgent
	}
	return UserAgent
}

func (h *HTTPPackageMeta) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (h *HTTPPackageMeta) Latest(ctx context.Context, registryType, identifier string) (string, time.Time, error) {
	switch strings.ToLower(registryType) {
	case RegistryNPM:
		return h.npmLatest(ctx, identifier)
	case RegistryPyPI:
		return h.pypiLatest(ctx, identifier)
	default:
		return "", time.Time{}, fmt.Errorf("%w", errUnpollable)
	}
}

var errUnpollable = fmt.Errorf("unpollable registry type")

func (h *HTTPPackageMeta) npmLatest(ctx context.Context, identifier string) (string, time.Time, error) {
	base := h.NPMBase
	if base == "" {
		base = "https://registry.npmjs.org"
	}
	pathID := identifier
	if strings.HasPrefix(identifier, "@") {
		pathID = url.PathEscape(identifier)
	}
	u := strings.TrimRight(base, "/") + "/" + pathID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("User-Agent", h.ua())
	req.Header.Set("Accept", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.StatusCode != 200 {
		return "", time.Time{}, &pollHTTPError{Status: resp.StatusCode}
	}
	var parsed struct {
		DistTags map[string]string `json:"dist-tags"`
		Time     map[string]string `json:"time"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("npm decode: %w", err)
	}
	latest := parsed.DistTags["latest"]
	if latest == "" {
		return "", time.Time{}, fmt.Errorf("npm: missing dist-tags.latest")
	}
	var pub time.Time
	if t := parsed.Time[latest]; t != "" {
		pub, _ = time.Parse(time.RFC3339, t)
	}
	return latest, pub, nil
}

func (h *HTTPPackageMeta) pypiLatest(ctx context.Context, identifier string) (string, time.Time, error) {
	base := h.PyPIBase
	if base == "" {
		base = "https://pypi.org/pypi"
	}
	u := strings.TrimRight(base, "/") + "/" + url.PathEscape(identifier) + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("User-Agent", h.ua())
	req.Header.Set("Accept", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.StatusCode != 200 {
		return "", time.Time{}, &pollHTTPError{Status: resp.StatusCode}
	}
	var parsed struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Releases map[string][]struct {
			UploadTime string `json:"upload_time_iso_8601"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("pypi decode: %w", err)
	}
	latest := parsed.Info.Version
	if latest == "" {
		return "", time.Time{}, fmt.Errorf("pypi: missing info.version")
	}
	var pub time.Time
	if files := parsed.Releases[latest]; len(files) > 0 && files[0].UploadTime != "" {
		pub, _ = time.Parse(time.RFC3339, files[0].UploadTime)
		if pub.IsZero() {
			pub, _ = time.Parse("2006-01-02T15:04:05", files[0].UploadTime)
		}
	}
	return latest, pub, nil
}

type pollHTTPError struct{ Status int }

func (e *pollHTTPError) Error() string {
	return fmt.Sprintf("package registry HTTP %d", e.Status)
}

// Pollable reports whether registryType supports a version poll.
func Pollable(registryType string) bool {
	switch strings.ToLower(registryType) {
	case RegistryNPM, RegistryPyPI:
		return true
	default:
		return false
	}
}

// PrimaryPackage returns the first package entry, if any.
func PrimaryPackage(s RegistryServer) (Package, bool) {
	if len(s.Packages) == 0 {
		return Package{}, false
	}
	return s.Packages[0], true
}

// HasPackages is true when the server is in the packages population.
func HasPackages(s RegistryServer) bool {
	return len(s.Packages) > 0
}

// RemoteOnly is true when the server has remotes but no packages.
func RemoteOnly(s RegistryServer) bool {
	return len(s.Packages) == 0 && len(s.Remotes) > 0
}

// PackageMetaClientFunc adapts a function to PackageMetaClient.
type PackageMetaClientFunc func(ctx context.Context, registryType, identifier string) (string, time.Time, error)

func (f PackageMetaClientFunc) Latest(ctx context.Context, registryType, identifier string) (string, time.Time, error) {
	return f(ctx, registryType, identifier)
}

// PollServer performs a tier-1 version poll for one packages-population server.
func PollServer(ctx context.Context, meta PackageMetaClient, srv RegistryServer, now time.Time) PollResult {
	pkg, ok := PrimaryPackage(srv)
	if !ok {
		return PollResult{
			ServerID:   srv.Name,
			PolledAt:   now,
			EventType:  EventRefused,
			Diagnostic: DiagNoTarget,
		}
	}
	res := PollResult{
		ServerID:      srv.Name,
		Identifier:    pkg.Identifier,
		RegistryType:  pkg.RegistryType,
		PinnedVersion: pkg.Version,
		PolledAt:      now,
	}
	if !Pollable(pkg.RegistryType) {
		res.EventType = EventUnpollable
		res.Diagnostic = DiagUnpollable
		return res
	}
	latest, pub, err := meta.Latest(ctx, pkg.RegistryType, pkg.Identifier)
	if err != nil {
		if he, ok := err.(*pollHTTPError); ok {
			st := he.Status
			res.HTTPStatus = &st
			switch {
			case st == 401 || st == 403:
				res.EventType = EventAuthRequired
				res.Diagnostic = DiagAuthRequired
			case st == 404:
				res.EventType = EventEndpoint404
				res.Diagnostic = DiagEndpoint404
			default:
				res.EventType = EventHTTPError
				res.Diagnostic = DiagHTTPError
			}
			return res
		}
		res.EventType = EventRefused
		res.Diagnostic = DiagRefused
		ls := strings.ToLower(err.Error())
		switch {
		case strings.Contains(ls, "timeout") || strings.Contains(ls, "deadline"):
			res.EventType = EventTimeout
			res.Diagnostic = DiagTimeout
		case strings.Contains(ls, "no such host") || strings.Contains(ls, "dns"):
			res.EventType = EventDNS
			res.Diagnostic = DiagDNS
		case strings.Contains(ls, "tls") || strings.Contains(ls, "certificate") || strings.Contains(ls, "x509"):
			res.EventType = EventTLS
			res.Diagnostic = DiagTLS
		case strings.Contains(ls, "decode") || strings.Contains(ls, "invalid"):
			res.EventType = EventMalformed
			res.Diagnostic = DiagMalformed
		}
		return res
	}
	res.LatestVersion = latest
	res.PublishedAt = pub
	res.Differ = pkg.Version != "" && latest != pkg.Version
	res.EventType = EventVersionPoll
	res.Diagnostic = DiagVersionPoll
	return res
}
