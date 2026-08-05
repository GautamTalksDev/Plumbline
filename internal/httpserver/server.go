// Package httpserver is the read-only Plumbline public log HTTP API.
package httpserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/changes"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Config configures the read-only server.
type Config struct {
	Log      *log.Log
	PubKey   ed25519.PublicKey
	Site     fs.FS // optional static site root
	RatePerM int   // requests per minute per IP; default 120
	CacheTTL time.Duration
}

// Server is a read-only transparency log HTTP handler.
type Server struct {
	cfg   Config
	mu    sync.Mutex
	cache map[string]cacheEntry
	rl    *rateLimiter
}

type cacheEntry struct {
	body    []byte
	ctype   string
	expires time.Time
}

// New returns a configured Server.
func New(cfg Config) *Server {
	if cfg.RatePerM <= 0 {
		cfg.RatePerM = 120
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	return &Server{
		cfg:   cfg,
		cache: map[string]cacheEntry{},
		rl:    newRateLimiter(cfg.RatePerM),
	}
}

// Handler returns the root HTTP handler (API + optional site).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /checkpoint", s.wrap(s.handleCheckpoint))
	mux.HandleFunc("GET /checkpoints", s.wrap(s.handleCheckpoints))
	mux.HandleFunc("GET /pubkey", s.wrap(s.handlePubkey))
	mux.HandleFunc("GET /entries", s.wrap(s.handleEntries))
	mux.HandleFunc("GET /proof/inclusion", s.wrap(s.handleInclusion))
	mux.HandleFunc("GET /proof/consistency", s.wrap(s.handleConsistency))
	mux.HandleFunc("GET /server/{id}/history", s.wrap(s.handleServerHistory))
	mux.HandleFunc("GET /server/history", s.wrap(s.handleServerHistoryQuery))
	mux.HandleFunc("GET /changes", s.wrap(s.handleChanges))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	var h http.Handler = mux
	if s.cfg.Site != nil {
		fileServer := http.FileServer(http.FS(s.cfg.Site))
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAPI(r.URL.Path) {
				mux.ServeHTTP(w, r)
				return
			}
			// Never cache HTML aggressively if it embeds live data via JS;
			// static assets can be long-cached by Cloudflare.
			if strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
				w.Header().Set("Cache-Control", "public, max-age=60")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			fileServer.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read-only: reject anything but GET/HEAD.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

type apiFunc func(http.ResponseWriter, *http.Request) (any, int, error)

func (s *Server) wrap(fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !s.rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		cacheKey := r.URL.Path + "?" + r.URL.RawQuery
		if body, ctype, ok := s.cacheGet(cacheKey); ok {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=30")
			w.Header().Set("X-Cache", "HIT")
			_, _ = w.Write(body)
			return
		}
		v, code, err := fn(w, r)
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
		if v == nil {
			return
		}
		body, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
			return
		}
		body = append(body, '\n')
		ctype := "application/json; charset=utf-8"
		s.cachePut(cacheKey, body, ctype)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}
}

func (s *Server) cacheGet(key string) ([]byte, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, "", false
	}
	return e.body, e.ctype, true
}

func (s *Server) cachePut(key string, body []byte, ctype string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bound cache size roughly.
	if len(s.cache) > 512 {
		s.cache = map[string]cacheEntry{}
	}
	s.cache[key] = cacheEntry{body: body, ctype: ctype, expires: time.Now().Add(s.cfg.CacheTTL)}
}

func (s *Server) handleCheckpoint(_ http.ResponseWriter, _ *http.Request) (any, int, error) {
	cp, ok, err := s.cfg.Log.LatestCheckpoint()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("no checkpoint")
	}
	return checkpointJSON(cp), http.StatusOK, nil
}

func (s *Server) handleCheckpoints(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cps, err := s.cfg.Log.ListCheckpoints(limit)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	out := make([]any, 0, len(cps))
	for _, cp := range cps {
		out = append(out, checkpointJSON(cp))
	}
	return map[string]any{"checkpoints": out}, http.StatusOK, nil
}

func (s *Server) handlePubkey(_ http.ResponseWriter, _ *http.Request) (any, int, error) {
	if len(s.cfg.PubKey) != ed25519.PublicKeySize {
		return nil, http.StatusInternalServerError, fmt.Errorf("pubkey not configured")
	}
	return map[string]any{
		"alg":        "ed25519",
		"public_key": hex.EncodeToString(s.cfg.PubKey),
		"key_id":     "plumbline-v1",
		"encoding":   "hex",
	}, http.StatusOK, nil
}

func (s *Server) handleEntries(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	from, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	if err != nil {
		from = 0
	}
	to, err := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if err != nil || to == 0 {
		to = from + 100
	}
	if to-from > 1000 {
		return nil, http.StatusBadRequest, fmt.Errorf("range too large (max 1000)")
	}
	ents, err := s.cfg.Log.Entries(from, to)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	out := make([]any, 0, len(ents))
	for _, e := range ents {
		out = append(out, entryJSON(e))
	}
	return map[string]any{"from": from, "to": to, "entries": out}, http.StatusOK, nil
}

func (s *Server) handleInclusion(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	idx, err := strconv.ParseInt(r.URL.Query().Get("idx"), 10, 64)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("idx required")
	}
	p, err := s.cfg.Log.InclusionProof(idx)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	path := make([]string, len(p.AuditPath))
	for i, h := range p.AuditPath {
		path[i] = hex.EncodeToString(h)
	}
	return map[string]any{
		"index":      p.Index,
		"tree_size":  p.TreeSize,
		"leaf_hash":  hex.EncodeToString(p.LeafHash),
		"audit_path": path,
		"root_hash":  hex.EncodeToString(p.RootHash),
	}, http.StatusOK, nil
}

func (s *Server) handleConsistency(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	oldS, err1 := strconv.ParseInt(r.URL.Query().Get("old"), 10, 64)
	newS, err2 := strconv.ParseInt(r.URL.Query().Get("new"), 10, 64)
	if err1 != nil || err2 != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("old and new required")
	}
	p, err := s.cfg.Log.ConsistencyProof(oldS, newS)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	path := make([]string, len(p.AuditPath))
	for i, h := range p.AuditPath {
		path[i] = hex.EncodeToString(h)
	}
	return map[string]any{
		"old_size":   p.OldSize,
		"new_size":   p.NewSize,
		"old_root":   hex.EncodeToString(p.OldRoot),
		"new_root":   hex.EncodeToString(p.NewRoot),
		"audit_path": path,
	}, http.StatusOK, nil
}

func (s *Server) handleServerHistory(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	id := r.PathValue("id")
	return s.serverHistory(id)
}

func (s *Server) handleServerHistoryQuery(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	id := r.URL.Query().Get("id")
	if id == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("id query parameter required")
	}
	return s.serverHistory(id)
}

func (s *Server) serverHistory(id string) (any, int, error) {
	if id == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("server id required")
	}
	ents, err := s.cfg.Log.EntriesByServer(id)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	out := make([]any, 0, len(ents))
	for _, e := range ents {
		out = append(out, entryJSON(e))
	}
	return map[string]any{"server_id": id, "entries": out}, http.StatusOK, nil
}

func (s *Server) handleChanges(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	sev, err := changes.ParseSeverity(r.URL.Query().Get("severity"))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	size, err := s.cfg.Log.Size()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	ents, err := s.cfg.Log.Entries(0, size)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	ch, err := changes.Detect(ents, changes.Options{Since: since, Severity: sev, Limit: 200})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"changes": ch}, http.StatusOK, nil
}

func checkpointJSON(cp log.Checkpoint) map[string]any {
	return map[string]any{
		"tree_size": cp.TreeSize,
		"root_hash": cp.RootHash,
		"timestamp": cp.Timestamp,
		"signature": cp.Signature,
	}
}

func entryJSON(e log.StoredEntry) map[string]any {
	return map[string]any{
		"idx":              e.Idx,
		"server_id":        e.ServerID,
		"tool_name":        e.ToolName,
		"hash":             e.Hash,
		"canonical":        base64.StdEncoding.EncodeToString(e.Canonical),
		"observed_at":      e.ObservedAt,
		"protocol_version": e.ProtocolVersion,
		"canon_version":    e.CanonVersion,
		"leaf_hash":        e.LeafHash,
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("CF-Connecting-IP"); xff != "" {
		return xff
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func isAPI(path string) bool {
	switch path {
	case "/checkpoint", "/checkpoints", "/pubkey", "/entries", "/changes", "/healthz", "/server/history":
		return true
	}
	if strings.HasPrefix(path, "/proof/") {
		return true
	}
	// /server/{id}/history — single-segment ids only
	if strings.HasPrefix(path, "/server/") && strings.HasSuffix(path, "/history") {
		return true
	}
	return false
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

func newRateLimiter(perMin int) *rateLimiter {
	return &rateLimiter{
		limit:   perMin,
		window:  time.Minute,
		buckets: map[string]*bucket{},
	}
}

func (r *rateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	b, ok := r.buckets[ip]
	if !ok || now.After(b.reset) {
		r.buckets[ip] = &bucket{count: 1, reset: now.Add(r.window)}
		return true
	}
	if b.count >= r.limit {
		return false
	}
	b.count++
	return true
}
