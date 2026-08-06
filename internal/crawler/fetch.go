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

// PackageFetcher is retained for tests that stage bytes; production uses PackageInstaller.
type PackageFetcher interface {
	Fetch(ctx context.Context, pkg Package, latestVersion string, destDir string) (localPath string, err error)
}

// HTTPPackageFetcher fetches npm tarballs / PyPI files with an allowlisted client.
// Prefer CLIPackageInstaller for tier-2; this remains for tooling/tests.
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
	f, err := os.Create(dest) //nolint:gosec // G304 -- dest is under the install workdir we created
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 64<<20)); err != nil {
		_ = f.Close()
		return err
	}
	// Close must be checked: a buffered write can fail at flush time, and a
	// silently truncated download becomes a mystery install failure.
	return f.Close()
}
