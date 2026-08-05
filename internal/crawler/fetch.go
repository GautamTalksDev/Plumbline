package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PackageFetcher downloads package bytes during the bounded fetch phase.
// Implementations must contact only package-registry hosts.
type PackageFetcher interface {
	Fetch(ctx context.Context, pkg Package, latestVersion string, destDir string) (localPath string, err error)
}

// HTTPPackageFetcher fetches npm tarballs / PyPI files with an allowlisted client.
type HTTPPackageFetcher struct {
	Client    *http.Client
	UserAgent string
	NPMBase   string
	PyPIBase  string
}

func (f *HTTPPackageFetcher) ua() string {
	if f.UserAgent != "" {
		return f.UserAgent
	}
	return UserAgent
}

func (f *HTTPPackageFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (f *HTTPPackageFetcher) Fetch(ctx context.Context, pkg Package, latestVersion string, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	ver := latestVersion
	if ver == "" {
		ver = pkg.Version
	}
	switch strings.ToLower(pkg.RegistryType) {
	case RegistryNPM:
		return f.fetchNPM(ctx, pkg.Identifier, ver, destDir)
	case RegistryPyPI:
		return f.fetchPyPI(ctx, pkg.Identifier, ver, destDir)
	default:
		return "", fmt.Errorf("fetch: unsupported registry %s", pkg.RegistryType)
	}
}

func (f *HTTPPackageFetcher) fetchNPM(ctx context.Context, identifier, version, destDir string) (string, error) {
	base := f.NPMBase
	if base == "" {
		base = "https://registry.npmjs.org"
	}
	pathID := identifier
	if strings.HasPrefix(identifier, "@") {
		pathID = url.PathEscape(identifier)
	}
	metaURL := strings.TrimRight(base, "/") + "/" + pathID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", f.ua())
	resp, err := f.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("npm meta HTTP %d", resp.StatusCode)
	}
	var meta struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", err
	}
	v, ok := meta.Versions[version]
	if !ok || v.Dist.Tarball == "" {
		return "", fmt.Errorf("npm: no tarball for %s@%s", identifier, version)
	}
	if err := assertAllowedPackageHost(v.Dist.Tarball, "registry.npmjs.org", "npm.pkg.github.com"); err != nil {
		return "", err
	}
	out := filepath.Join(destDir, "package.tgz")
	if err := downloadFile(ctx, f.client(), f.ua(), v.Dist.Tarball, out); err != nil {
		return "", err
	}
	return out, nil
}

func (f *HTTPPackageFetcher) fetchPyPI(ctx context.Context, identifier, version, destDir string) (string, error) {
	base := f.PyPIBase
	if base == "" {
		base = "https://pypi.org/pypi"
	}
	u := strings.TrimRight(base, "/") + "/" + url.PathEscape(identifier) + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", f.ua())
	resp, err := f.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("pypi meta HTTP %d", resp.StatusCode)
	}
	var meta struct {
		Releases map[string][]struct {
			URL         string `json:"url"`
			Packagetype string `json:"packagetype"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", err
	}
	files := meta.Releases[version]
	if len(files) == 0 {
		return "", fmt.Errorf("pypi: no files for %s==%s", identifier, version)
	}
	fileURL := files[0].URL
	for _, fl := range files {
		if fl.Packagetype == "sdist" {
			fileURL = fl.URL
			break
		}
	}
	if err := assertAllowedPackageHost(fileURL, "files.pythonhosted.org", "pypi.org"); err != nil {
		return "", err
	}
	out := filepath.Join(destDir, "package.pypi")
	if err := downloadFile(ctx, f.client(), f.ua(), fileURL, out); err != nil {
		return "", err
	}
	return out, nil
}

func assertAllowedPackageHost(raw string, allowed ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return nil
		}
	}
	return fmt.Errorf("fetch: host %q not allowlisted", host)
}

func downloadFile(ctx context.Context, client *http.Client, ua, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 64<<20))
	return err
}

// observePackageFetchThenSpawn downloads the package (allowlisted hosts only),
// then runs tools/list inside the existing sandbox with default-deny network.
func observePackageFetchThenSpawn(ctx context.Context, sb Sandbox, fetcher PackageFetcher, srv RegistryServer, latest string) Observation {
	pkg, ok := PrimaryPackage(srv)
	if !ok {
		return observationFromClassified(srv.Name, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if !Pollable(pkg.RegistryType) {
		return observationFromClassified(srv.Name, Classified{Event: EventUnpollable, Diagnostic: DiagUnpollable})
	}
	if fetcher == nil {
		return observationFromClassified(srv.Name, Classified{Event: EventFetchError, Diagnostic: DiagFetchError})
	}
	dir, err := os.MkdirTemp("", "plumbline-fetch-*")
	if err != nil {
		return observationFromClassified(srv.Name, Classified{Event: EventFetchError, Diagnostic: DiagFetchError})
	}
	defer os.RemoveAll(dir)

	localPath, err := fetcher.Fetch(ctx, pkg, latest, dir)
	if err != nil {
		return observationFromClassified(srv.Name, Classified{Event: EventFetchError, Diagnostic: DiagFetchError})
	}

	// Spawn uses the existing sandbox unchanged (network none). The staged
	// package path is passed as the command target for the observe helper.
	t := Target{
		ServerID: srv.Name,
		Command:  localPath,
		Args:     []string{"--plumbline-package", pkg.RegistryType, pkg.Identifier, latest},
		Env:      []string{"PLUMBLINE_PACKAGE_FILE=" + localPath},
		Timeout:  60 * time.Second,
	}
	return sb.Observe(ctx, t)
}
