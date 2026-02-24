package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ranausmanai/acl/internal/manifest"
	"github.com/ranausmanai/acl/internal/packs"
	"github.com/ranausmanai/acl/internal/receipt"
	"github.com/ranausmanai/acl/internal/runtime"
	"github.com/ranausmanai/acl/internal/store"
)

type platformEvent struct {
	Type          string         `json:"type"`
	PlatformRunID string         `json:"platform_run_id,omitempty"`
	RunID         int64          `json:"run_id,omitempty"`
	Timestamp     string         `json:"timestamp"`
	Data          map[string]any `json:"data,omitempty"`
}

type platformEventHub struct {
	mu   sync.RWMutex
	subs map[chan platformEvent]struct{}
}

func newPlatformEventHub() *platformEventHub {
	return &platformEventHub{subs: map[chan platformEvent]struct{}{}}
}
func (h *platformEventHub) Subscribe() chan platformEvent {
	ch := make(chan platformEvent, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}
func (h *platformEventHub) Unsubscribe(ch chan platformEvent) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}
func (h *platformEventHub) Publish(e platformEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

type approvalRequest struct {
	ID           string         `json:"id"`
	Pack         string         `json:"pack"`
	File         string         `json:"file"`
	Vars         map[string]any `json:"vars"`
	GeneratedACL string         `json:"generated_acl,omitempty"`
	PreviewRunID int64          `json:"preview_run_id,omitempty"`
	Status       string         `json:"status"`
	CreatedAt    string         `json:"created_at"`
	ResolvedAt   string         `json:"resolved_at,omitempty"`
}

type approvalStore struct {
	mu    sync.RWMutex
	items map[string]*approvalRequest
}

func newApprovalStore() *approvalStore { return &approvalStore{items: map[string]*approvalRequest{}} }
func (s *approvalStore) Put(a *approvalRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	s.items[a.ID] = &cp
}
func (s *approvalStore) Get(id string) (*approvalRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.items[id]
	if !ok {
		return nil, false
	}
	cp := *a
	if a.Vars != nil {
		cp.Vars = cloneMap(a.Vars)
	}
	return &cp, true
}
func (s *approvalStore) Update(id string, fn func(*approvalRequest)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	if !ok {
		return false
	}
	fn(a)
	return true
}
func (s *approvalStore) List() []*approvalRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*approvalRequest, 0, len(s.items))
	for _, a := range s.items {
		cp := *a
		cp.Vars = nil
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}
func (s *approvalStore) PendingCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, a := range s.items {
		if a.Status == "pending" {
			n++
		}
	}
	return n
}

type credentialEntry struct {
	Name      string `json:"name"`
	Provider  string `json:"provider,omitempty"`
	Value     string `json:"-"`
	UpdatedAt string `json:"updated_at"`
}

type credentialStore struct {
	mu    sync.RWMutex
	path  string
	items map[string]credentialEntry
}

func newCredentialStore() (*credentialStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".acl")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	cs := &credentialStore{path: filepath.Join(dir, "platform_credentials.json"), items: map[string]credentialEntry{}}
	_ = cs.load()
	return cs, nil
}
func newCredentialStoreInMemory() *credentialStore {
	return &credentialStore{items: map[string]credentialEntry{}}
}
func (s *credentialStore) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var arr []credentialEntry
	if err := json.Unmarshal(b, &arr); err != nil {
		return err
	}
	for _, c := range arr {
		s.items[c.Name] = c
	}
	return nil
}
func (s *credentialStore) persist() error {
	if s.path == "" {
		return nil
	}
	arr := make([]credentialEntry, 0, len(s.items))
	for _, c := range s.items {
		arr = append(arr, c)
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Name < arr[j].Name })
	b, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
func (s *credentialStore) ListMasked() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.items))
	for _, c := range s.items {
		out = append(out, map[string]any{"name": c.Name, "provider": c.Provider, "updated_at": c.UpdatedAt, "masked": true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}
func (s *credentialStore) Count() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.items) }
func (s *credentialStore) Upsert(name, provider, value string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("name and value required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[name] = credentialEntry{Name: name, Provider: provider, Value: value, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	return s.persist()
}
func (s *credentialStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
	return s.persist()
}
func (s *credentialStore) Get(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[name]
	return c.Value, ok
}

func (s *Server) handlePlatformRunsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	bw := bufio.NewWriter(w)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	fmt.Fprintf(bw, "data: %s\n\n", `{"type":"stream.connected","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`"}`)
	bw.Flush()
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(bw, "data: %s\n\n", b)
			bw.Flush()
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(bw, ": keepalive\n\n")
			bw.Flush()
			flusher.Flush()
		}
	}
}

func (s *Server) handlePlatformOverview(w http.ResponseWriter, _ *http.Request) {
	runsCount := int64(0)
	if st, err := openHistoryStore(); err == nil {
		runsCount, _ = st.Count()
		st.Close()
	}
	ps, _ := packs.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"runs_count":        runsCount,
		"pending_approvals": s.approvals.PendingCount(),
		"builtin_packs":     len(ps),
		"credentials":       s.creds.Count(),
	})
}

func (s *Server) handlePlatformRuns(w http.ResponseWriter, r *http.Request) {
	limit := 25
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	st, err := openHistoryStore()
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	defer st.Close()
	sums, err := st.List(limit)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, sums)
}

func (s *Server) handlePlatformRunByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	st, err := openHistoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer st.Close()
	rec, err := st.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "receipt": rec})
}

func (s *Server) handlePlatformPacks(w http.ResponseWriter, _ *http.Request) {
	ps, err := packs.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) handlePlatformPacksInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Output string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Output == "" {
		body.Output = filepath.Join(".", "acl-packs")
	}
	res, err := packs.Install(body.Name, body.Output)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handlePlatformManifests(w http.ResponseWriter, _ *http.Request) {
	ps, err := packs.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []map[string]any{}
	for _, p := range ps {
		if p.ToolManifest == "" {
			continue
		}
		b, err := packs.ReadFile(p.Name, p.ToolManifest)
		if err != nil {
			continue
		}
		m, err := manifest.ParseBytes(b)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"id": p.Name + ":" + p.ToolManifest, "pack": p.Name, "provider": m.Provider, "base_url": m.BaseURL, "tools_count": len(m.Tools)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePlatformManifestByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "manifest id must be pack:file")
		return
	}
	b, err := packs.ReadFile(parts[0], parts[1])
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

func (s *Server) handlePlatformTemplates(w http.ResponseWriter, _ *http.Request) {
	ps, err := packs.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []map[string]any{}
	for _, p := range ps {
		for _, t := range p.Templates {
			b, _ := packs.ReadFile(p.Name, t.File)
			out = append(out, map[string]any{
				"id":          p.Name + ":" + t.File,
				"pack":        p.Name,
				"name":        t.Name,
				"file":        t.File,
				"description": t.Description,
				"source_acl":  string(b),
				"sample_vars": sampleVarsForTemplate(p.Name, t.File),
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func sampleVarsForTemplate(packName, file string) map[string]any {
	if strings.Contains(packName, "extractly") {
		return map[string]any{
			"api_key":      "YOUR_EXTRACTLY_API_KEY",
			"base_url":     "https://extractly.me/api",
			"request_json": `{"url":"https://example.com","maxPages":5,"mode":"optimized"}`,
			"approved":     false,
		}
	}
	if strings.Contains(file, "zapier") {
		return map[string]any{"approved": false}
	}
	return map[string]any{"approved": false}
}

func (s *Server) handlePlatformTemplatePreview(w http.ResponseWriter, r *http.Request) {
	s.runPlatformTemplate(w, r, true)
}
func (s *Server) handlePlatformTemplateRun(w http.ResponseWriter, r *http.Request) {
	s.runPlatformTemplate(w, r, false)
}

func (s *Server) runPlatformTemplate(w http.ResponseWriter, r *http.Request, preview bool) {
	var body struct {
		Pack string         `json:"pack"`
		File string         `json:"file"`
		Vars map[string]any `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Pack == "" || body.File == "" {
		writeError(w, http.StatusBadRequest, "pack and file are required")
		return
	}
	src, err := packs.ReadFile(body.Pack, body.File)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if body.Vars == nil {
		body.Vars = map[string]any{}
	}
	// Credential convenience: if api_key is empty and pack declares a required env, inject from stored credentials.
	if key, _ := body.Vars["api_key"].(string); strings.TrimSpace(key) == "" {
		if meta, err := packs.Info(body.Pack); err == nil {
			for _, e := range meta.Env {
				if e.Required {
					if v, ok := s.creds.Get(e.Name); ok {
						body.Vars["api_key"] = v
						break
					}
				}
			}
		}
	}
	if preview {
		if _, ok := body.Vars["approved"]; !ok {
			body.Vars["approved"] = false
		}
	}
	rec, runID, perr := s.executePlatformSource(r.Context(), string(src), body.Vars)
	if perr != nil && rec == nil {
		writeError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	resp := map[string]any{"receipt": rec, "generated_acl": string(src), "run_id": runID}
	if preview {
		id := randID()
		s.approvals.Put(&approvalRequest{ID: id, Pack: body.Pack, File: body.File, Vars: cloneMap(body.Vars), GeneratedACL: string(src), PreviewRunID: runID, Status: "pending", CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		resp["preview_id"] = id
		s.events.Publish(platformEvent{Type: "approval.requested", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Data: map[string]any{"approval_id": id, "pack": body.Pack, "file": body.File}})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePlatformApprovals(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.approvals.List())
}

func (s *Server) handlePlatformApprovalApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, ok := s.approvals.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}
	if req.Status != "pending" {
		writeError(w, http.StatusBadRequest, "approval is not pending")
		return
	}
	src, err := packs.ReadFile(req.Pack, req.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	vars := cloneMap(req.Vars)
	vars["approved"] = true
	rec, runID, perr := s.executePlatformSource(r.Context(), string(src), vars)
	if perr != nil && rec == nil {
		writeError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	s.approvals.Update(id, func(a *approvalRequest) { a.Status = "approved"; a.ResolvedAt = time.Now().UTC().Format(time.RFC3339) })
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "receipt": rec, "run_id": runID})
}

func (s *Server) handlePlatformApprovalReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.approvals.Update(id, func(a *approvalRequest) { a.Status = "rejected"; a.ResolvedAt = time.Now().UTC().Format(time.RFC3339) }) {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "rejected"})
}

func (s *Server) handlePlatformCredentials(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.creds.ListMasked())
}

func (s *Server) handlePlatformCredentialUpsert(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Provider, Value string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.creds.Upsert(body.Name, body.Provider, body.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePlatformCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.creds.Delete(r.PathValue("name")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) executePlatformSource(ctx context.Context, src string, vars map[string]any) (*receipt.Receipt, int64, error) {
	platformRunID := randID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.events.Publish(platformEvent{Type: "run.started", PlatformRunID: platformRunID, Timestamp: now})
	rec, err := runtime.RunSource(ctx, src, runtime.Config{Vars: vars, OnEvent: func(ev runtime.Event) {
		data := map[string]any{"agent": ev.Agent, "step": ev.Step, "target": ev.Target}
		if ev.Trace != nil {
			data["duration_ms"] = ev.Trace.DurationMS
			data["status"] = map[bool]string{true: "failed", false: "success"}[ev.Trace.Error != ""]
		}
		s.events.Publish(platformEvent{Type: ev.Type, PlatformRunID: platformRunID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Data: data})
	}}, s.reg)
	var runID int64
	if rec != nil && os.Getenv("ACL_NO_HISTORY") == "" {
		if st, e := openHistoryStore(); e == nil {
			runID, _ = st.Put(rec)
			st.Close()
		}
	}
	finishData := map[string]any{"status": "failed"}
	if rec != nil {
		finishData["status"] = rec.Status
	}
	if runID != 0 {
		finishData["run_id"] = runID
	}
	if err != nil {
		finishData["error"] = err.Error()
	}
	s.events.Publish(platformEvent{Type: "run.finished", PlatformRunID: platformRunID, RunID: runID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Data: finishData})
	return rec, runID, err
}

func openHistoryStore() (*store.Store, error) {
	dbPath, err := store.DefaultPath()
	if err != nil {
		return nil, err
	}
	return store.Open(dbPath)
}

func randID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
