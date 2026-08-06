package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Installable reports whether registryType can be installed for tier-2 spawn.
// Only npm and PyPI are supported; oci/mcpb have no install path.
func Installable(registryType string) bool {
	return Pollable(registryType)
}

// InstallResult is a successfully installed package ready for sandboxed spawn.
type InstallResult struct {
	WorkDir      string   // install prefix / target directory
	Command      string   // host runtime binary name or path ("node", "python3")
	Args         []string // args after Command (entrypoint path, -m module, …)
	Env          []string // extra env (e.g. PYTHONPATH)
	Entrypoint   string   // resolved entrypoint recorded in the observation
	RegistryType string
	Identifier   string
	Version      string
}

// PackageInstaller installs a package during the network-open fetch phase.
type PackageInstaller interface {
	Install(ctx context.Context, pkg Package, version string, destDir string) (InstallResult, error)
}

// ErrInstallFailed is returned when npm/pip exits non-zero.
var ErrInstallFailed = errors.New("crawler: package install failed")

// ErrEntrypointUnresolved is returned when install succeeded but no runnable entrypoint exists.
var ErrEntrypointUnresolved = errors.New("crawler: entrypoint unresolved")

// CLIPackageInstaller runs npm/pip with lifecycle scripts disabled.
type CLIPackageInstaller struct {
	NPM  string // default "npm"
	Pip  string // default "pip3" then "pip"
	Node string // default "node" (unused at install; spawn resolves)
}

func (i *CLIPackageInstaller) npmBin() string {
	if i.NPM != "" {
		return i.NPM
	}
	return "npm"
}

func (i *CLIPackageInstaller) pipBin() string {
	if i.Pip != "" {
		return i.Pip
	}
	if p, err := exec.LookPath("pip3"); err == nil {
		return p
	}
	return "pip"
}

// Install runs a real dependency-closure install with --ignore-scripts (npm)
// or --no-input (pip). Never runs package lifecycle scripts.
func (i *CLIPackageInstaller) Install(ctx context.Context, pkg Package, version string, destDir string) (InstallResult, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return InstallResult{}, err
	}
	ver := version
	if ver == "" {
		ver = pkg.Version
	}
	switch strings.ToLower(pkg.RegistryType) {
	case RegistryNPM:
		res, err := i.installNPM(ctx, pkg.Identifier, ver, destDir)
		if err != nil {
			return res, err
		}
		res.Args = appendPackageRuntimeArgs(res.Args, pkg)
		return res, nil
	case RegistryPyPI:
		res, err := i.installPyPI(ctx, pkg.Identifier, ver, destDir)
		if err != nil {
			return res, err
		}
		res.Args = appendPackageRuntimeArgs(res.Args, pkg)
		return res, nil
	default:
		return InstallResult{}, fmt.Errorf("install: unsupported registry %s", pkg.RegistryType)
	}
}

func (i *CLIPackageInstaller) installNPM(ctx context.Context, identifier, version, destDir string) (InstallResult, error) {
	spec := identifier
	local := isLocalPackagePath(identifier)
	if !local && version != "" {
		spec = identifier + "@" + version
	}
	// --ignore-scripts is mandatory: lifecycle scripts run stranger code
	// during the network-open window.
	cmd := exec.CommandContext(ctx, i.npmBin(), //nolint:gosec // G204 -- npm install of a registry identifier during fetch; --ignore-scripts mandatory
		"install", "--prefix", destDir,
		"--ignore-scripts",
		"--no-fund",
		"--no-audit",
		"--no-update-notifier",
		spec,
	)
	cmd.Env = append(os.Environ(), "npm_config_ignore_scripts=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: npm: %v (%s)", ErrInstallFailed, err, truncateOut(out, 200))
	}

	pkgName := identifier
	if local {
		n, err := readPackageName(identifier)
		if err != nil {
			return InstallResult{}, fmt.Errorf("%w: %v", ErrEntrypointUnresolved, err)
		}
		pkgName = n
	}

	ep, err := resolveNPMBin(destDir, pkgName)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrEntrypointUnresolved, err)
	}
	rel, err := filepath.Rel(destDir, ep)
	if err != nil {
		rel = ep
	}
	return InstallResult{
		WorkDir:      destDir,
		Command:      "node",
		Args:         []string{ep},
		Entrypoint:   filepath.ToSlash(rel),
		RegistryType: RegistryNPM,
		Identifier:   pkgName,
		Version:      version,
	}, nil
}

func isLocalPackagePath(identifier string) bool {
	if strings.HasPrefix(identifier, ".") || strings.HasPrefix(identifier, "/") {
		return true
	}
	if strings.HasPrefix(identifier, "file:") {
		return true
	}
	if strings.HasSuffix(identifier, ".tgz") || strings.HasSuffix(identifier, ".tar.gz") {
		return true
	}
	return false
}

func readPackageName(dirOrTgz string) (string, error) {
	dirOrTgz = strings.TrimPrefix(dirOrTgz, "file:")
	if strings.HasSuffix(dirOrTgz, ".tgz") || strings.HasSuffix(dirOrTgz, ".tar.gz") {
		// Prefer name from filename: plumbline-fake-mcp-1.0.0.tgz
		base := filepath.Base(dirOrTgz)
		base = strings.TrimSuffix(base, ".tar.gz")
		base = strings.TrimSuffix(base, ".tgz")
		// Strip trailing -version (last -N.N.N)
		if i := strings.LastIndex(base, "-"); i > 0 {
			return base[:i], nil
		}
		return base, nil
	}
	raw, err := os.ReadFile(filepath.Join(dirOrTgz, "package.json")) //nolint:gosec // G304 -- path under install prefix we control
	if err != nil {
		return "", err
	}
	var meta struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", err
	}
	if meta.Name == "" {
		return "", fmt.Errorf("package.json missing name in %s", dirOrTgz)
	}
	return meta.Name, nil
}

func (i *CLIPackageInstaller) installPyPI(ctx context.Context, identifier, version, destDir string) (InstallResult, error) {
	spec := identifier
	if version != "" {
		spec = identifier + "==" + version
	}
	cmd := exec.CommandContext(ctx, i.pipBin(), //nolint:gosec // G204 -- pip install of a registry identifier during fetch; no scripts invoked
		"install", "--target", destDir,
		"--no-input",
		"--disable-pip-version-check",
		spec,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: pip: %v (%s)", ErrInstallFailed, err, truncateOut(out, 200))
	}

	epArgs, epLabel, err := resolvePyPIEntrypoint(destDir, identifier)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrEntrypointUnresolved, err)
	}
	return InstallResult{
		WorkDir:      destDir,
		Command:      "python3",
		Args:         epArgs,
		Env:          []string{"PYTHONPATH=" + destDir},
		Entrypoint:   epLabel,
		RegistryType: RegistryPyPI,
		Identifier:   identifier,
		Version:      version,
	}, nil
}

// resolveNPMBin reads the installed package's package.json and returns an
// absolute path to the bin entry.
func resolveNPMBin(prefix, identifier string) (string, error) {
	pkgDir := filepath.Join(prefix, "node_modules", filepath.FromSlash(identifier))
	raw, err := os.ReadFile(filepath.Join(pkgDir, "package.json")) //nolint:gosec // G304 -- path under install prefix we control
	if err != nil {
		return "", fmt.Errorf("read package.json: %w", err)
	}
	var meta struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", fmt.Errorf("parse package.json: %w", err)
	}
	if len(meta.Bin) == 0 || string(meta.Bin) == "null" {
		return "", fmt.Errorf("package %s has no bin", identifier)
	}

	var rel string
	var asStr string
	if err := json.Unmarshal(meta.Bin, &asStr); err == nil && asStr != "" {
		rel = asStr
	} else {
		var asMap map[string]string
		if err := json.Unmarshal(meta.Bin, &asMap); err != nil || len(asMap) == 0 {
			return "", fmt.Errorf("package %s has unusable bin field", identifier)
		}
		name := meta.Name
		if name == "" {
			name = identifier
		}
		if v, ok := asMap[name]; ok {
			rel = v
		} else if i := strings.LastIndex(name, "/"); i >= 0 {
			if v, ok := asMap[name[i+1:]]; ok {
				rel = v
			}
		}
		if rel == "" {
			for _, v := range asMap {
				rel = v
				break
			}
		}
	}
	if rel == "" {
		return "", fmt.Errorf("package %s: empty bin path", identifier)
	}
	abs := filepath.Join(pkgDir, filepath.FromSlash(rel))
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return "", fmt.Errorf("bin %s missing", abs)
	}
	return abs, nil
}

// resolvePyPIEntrypoint selects a console script from the *target* package's
// own dist-info only (PEP 503 name match). Never selects a dependency's script.
func resolvePyPIEntrypoint(target, identifier string) (args []string, label string, err error) {
	want := normalizePEP503(identifier)
	module := strings.ReplaceAll(identifier, "-", "_")
	matches, _ := filepath.Glob(filepath.Join(target, "*.dist-info", "entry_points.txt"))

	var targetScripts []consoleScript
	for _, ep := range matches {
		distBase := filepath.Base(filepath.Dir(ep)) // e.g. tablestakes-1.1.0.dist-info
		pkgName := distInfoPackageName(distBase)
		if normalizePEP503(pkgName) != want {
			continue
		}
		scripts, parseErr := parseConsoleScripts(ep)
		if parseErr != nil || len(scripts) == 0 {
			continue
		}
		targetScripts = append(targetScripts, scripts...)
	}

	if len(targetScripts) > 0 {
		obj := targetScripts[0]
		for _, s := range targetScripts {
			if normalizePEP503(s.name) == want || s.name == module || s.name == identifier {
				obj = s
				break
			}
		}
		// Strip extras markers like "mcp.cli:app [cli]"
		attr := obj.attr
		if i := strings.IndexAny(attr, " \t["); i >= 0 {
			attr = strings.TrimSpace(attr[:i])
		}
		code := fmt.Sprintf("import sys; from %s import %s as _e; sys.exit(_e())", obj.module, attr)
		return []string{"-c", code}, obj.name, nil
	}

	// Fallback: python -m <module> when the package is importable as a module.
	initPath := filepath.Join(target, module, "__init__.py")
	if _, e := os.Stat(initPath); e == nil {
		mainPath := filepath.Join(target, module, "__main__.py")
		if _, e2 := os.Stat(mainPath); e2 == nil {
			return []string{"-m", module}, module, nil
		}
		// Module exists; try -m anyway (some packages run via -m without __main__).
		return []string{"-m", module}, module, nil
	}
	return nil, "", fmt.Errorf("no console_scripts or runnable module for %s", identifier)
}

// appendPackageRuntimeArgs appends fixed server.json packageArguments and
// applicable runtimeArguments onto the resolved entrypoint argv.
// runtimeArguments that begin with the package/script name (uvx/npx style)
// drop that leading token; the remainder is treated as package args.
func appendPackageRuntimeArgs(base []string, pkg Package) []string {
	extra := append([]string(nil), pkg.PackageArgs...)
	rt := pkg.RuntimeArgs
	if len(rt) > 0 {
		first := rt[0]
		idBase := pkg.Identifier
		if i := strings.LastIndex(idBase, "/"); i >= 0 {
			idBase = idBase[i+1:]
		}
		if first == pkg.Identifier || first == idBase || first == normalizePEP503(pkg.Identifier) ||
			first == strings.ReplaceAll(pkg.Identifier, "-", "_") {
			rt = rt[1:]
		}
		extra = append(extra, rt...)
	}
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// normalizePEP503 lowercases and maps run of [-_.] to a single '-' (PEP 503).
func normalizePEP503(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		b.WriteRune(r)
		prevDash = false
	}
	return b.String()
}

// distInfoPackageName extracts the distribution name from a *.dist-info directory base.
func distInfoPackageName(base string) string {
	base = strings.TrimSuffix(base, ".dist-info")
	for i := len(base) - 1; i > 0; i-- {
		if base[i] == '-' && i+1 < len(base) && base[i+1] >= '0' && base[i+1] <= '9' {
			return base[:i]
		}
	}
	return base
}

type consoleScript struct {
	name, module, attr string
}

func parseConsoleScripts(path string) ([]consoleScript, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304 -- METADATA path under pip install target we control
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	inSection := false
	var out []consoleScript
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = strings.EqualFold(line, "[console_scripts]")
			continue
		}
		if !inSection {
			continue
		}
		// name = module:attr
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		modAttr := strings.SplitN(rhs, ":", 2)
		if len(modAttr) != 2 {
			continue
		}
		out = append(out, consoleScript{
			name:   name,
			module: strings.TrimSpace(modAttr[0]),
			attr:   strings.TrimSpace(modAttr[1]),
		})
	}
	return out, nil
}

func truncateOut(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// observePackageInstallThenSpawn installs (network open), then spawns inside
// the sandbox with network denied. Never execs an archive as argv0.
func observePackageInstallThenSpawn(ctx context.Context, sb Sandbox, installer PackageInstaller, srv RegistryServer, latest string) Observation {
	pkg, ok := PrimaryPackage(srv)
	if !ok {
		return observationFromClassified(srv.Name, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if !Installable(pkg.RegistryType) {
		// Non-installable types must not reach tier 2; refuse rather than
		// emit the tier-1 class "unpollable".
		return observationFromClassified(srv.Name, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if installer == nil {
		return observationFromClassified(srv.Name, Classified{Event: EventInstallFailed, Diagnostic: DiagInstallFailed})
	}

	dir, err := os.MkdirTemp("", "plumbline-install-*")
	if err != nil {
		return observationFromClassified(srv.Name, Classified{Event: EventInstallFailed, Diagnostic: DiagInstallFailed})
	}
	defer os.RemoveAll(dir)

	installCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	res, err := installer.Install(installCtx, pkg, latest, dir)
	if err != nil {
		switch {
		case errors.Is(err, ErrEntrypointUnresolved):
			obs := observationFromClassified(srv.Name, Classified{Event: EventEntrypointUnresolved, Diagnostic: DiagEntrypointUnresolved})
			obs.Identifier = pkg.Identifier
			obs.RegistryType = pkg.RegistryType
			obs.LatestVersion = latest
			return obs
		default:
			obs := observationFromClassified(srv.Name, Classified{Event: EventInstallFailed, Diagnostic: DiagInstallFailed})
			obs.Identifier = pkg.Identifier
			obs.RegistryType = pkg.RegistryType
			obs.LatestVersion = latest
			return obs
		}
	}

	t := Target{
		ServerID: srv.Name,
		Command:  res.Command,
		Args:     res.Args,
		Env:      res.Env,
		WorkDir:  res.WorkDir,
		SpeakMCP: true,
		Timeout:  60 * time.Second,
	}
	obs := sb.Observe(ctx, t)
	obs.Identifier = res.Identifier
	obs.RegistryType = res.RegistryType
	obs.LatestVersion = res.Version
	obs.Entrypoint = res.Entrypoint
	return obs
}
