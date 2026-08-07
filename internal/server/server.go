// Package server implements the comms-net v1 HTTP/SSE hub as a zero-dependency
// Go server. It speaks the wire contract defined by ref/src/coms-net-server.ts.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agent statuses.
const (
	StatusOnline  = "online"
	StatusStale   = "stale"
	StatusOffline = "offline"
)

// Message statuses (wire enum; never "completed"/"expired").
const (
	MessageQueued    = "queued"
	MessageDelivered = "delivered"
	MessageComplete  = "complete"
	MessageError     = "error"
	MessageTimeout   = "timeout"
)

// Defaults for configurable durations.
const (
	DefaultAwaitTimeout      = 30 * time.Second
	DefaultKeepaliveInterval = 15 * time.Second
	DefaultStaleScanInterval = 5 * time.Second
	DefaultTTLScanInterval   = 10 * time.Second
)

// isoLayout matches the reference's new Date().toISOString() (UTC, ms, Z).
const isoLayout = "2006-01-02T15:04:05.000Z"

// Config holds all tunables for a hub. Zero values fall back to defaults.
type Config struct {
	Project        string
	Host           string
	Port           int
	PublicURL      string
	Token          string
	TokenFileOwned bool

	MaxHops           int
	MessageTTL        time.Duration
	MaxInbox          int
	HeartbeatInterval time.Duration
	StaleAfter        time.Duration
	OfflineAfter      time.Duration
	StaleScanInterval time.Duration
	TTLScanInterval   time.Duration
	KeepaliveInterval time.Duration

	LogQuiet     bool
	LogHeartbeat bool
}

// DefaultConfig returns the reference-compatible defaults.
func DefaultConfig() Config {
	return Config{
		Project:           "default",
		Host:              "127.0.0.1",
		Port:              0,
		MaxHops:           5,
		MessageTTL:        30 * time.Minute,
		MaxInbox:          100,
		HeartbeatInterval: 10 * time.Second,
		StaleAfter:        30 * time.Second,
		OfflineAfter:      60 * time.Second,
		StaleScanInterval: DefaultStaleScanInterval,
		TTLScanInterval:   DefaultTTLScanInterval,
		KeepaliveInterval: DefaultKeepaliveInterval,
	}
}

// AgentCard is the wire shape of an agent as seen by peers and clients.
type AgentCard struct {
	SessionID      string  `json:"session_id"`
	Name           string  `json:"name"`
	Purpose        string  `json:"purpose"`
	Model          string  `json:"model"`
	Provider       *string `json:"provider,omitempty"`
	Color          string  `json:"color"`
	Cwd            string  `json:"cwd"`
	Project        string  `json:"project"`
	Explicit       bool    `json:"explicit"`
	StartedAt      string  `json:"started_at"`
	ContextUsedPct int     `json:"context_used_pct"`
	QueueDepth     int     `json:"queue_depth"`
	Status         string  `json:"status"`
}

// RegistryEntry is an AgentCard plus liveness bookkeeping.
type RegistryEntry struct {
	AgentCard
	LastSeenAt   string `json:"last_seen_at"`
	RegisteredAt string `json:"registered_at"`
}

// Card returns the wire card for this entry.
func (e *RegistryEntry) Card() AgentCard {
	return e.AgentCard
}

// ComsMessage is a message in the queue.
type ComsMessage struct {
	MsgID          string  `json:"msg_id"`
	Project        string  `json:"project"`
	SenderSession  string  `json:"sender_session"`
	TargetSession  string  `json:"target_session"`
	Prompt         string  `json:"prompt"`
	ConversationID any     `json:"conversation_id"`
	ResponseSchema any     `json:"response_schema"`
	Hops           int     `json:"hops"`
	Status         string  `json:"status"`
	Response       any     `json:"response"`
	Error          *string `json:"error"`
	CreatedAt      string  `json:"created_at"`
	DeliveredAt    string  `json:"delivered_at,omitempty"`
	CompletedAt    string  `json:"completed_at,omitempty"`
	ExpiresAt      string  `json:"expires_at"`
}

// view is the wire shape returned by GET /v1/messages/:id and /await.
func (m *ComsMessage) view() map[string]any {
	return map[string]any{
		"msg_id":   m.MsgID,
		"status":   m.Status,
		"response": m.Response,
		"error":    m.Error,
	}
}

// sseWriter is one agent's outbound SSE stream.
type sseWriter struct {
	ch     chan string
	lastID int64
}

// Server is a single-project comms-net hub.
type Server struct {
	cfg       Config
	id        string
	startedAt time.Time

	mu        sync.Mutex
	agents    map[string]*RegistryEntry
	nameIndex map[string]map[string]struct{}
	messages  map[string]*ComsMessage
	awaiters  map[string][]chan *ComsMessage
	streams   map[string]*sseWriter
	boundPort int

	stopCh       chan struct{}
	loopsWG      sync.WaitGroup
	loopsStarted bool
	stopOnce     sync.Once
	httpServer   *http.Server
}

// NewServer creates a hub for the configured project.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:       cfg,
		id:        ulid(),
		startedAt: time.Now().UTC(),
		agents:    make(map[string]*RegistryEntry),
		nameIndex: make(map[string]map[string]struct{}),
		messages:  make(map[string]*ComsMessage),
		awaiters:  make(map[string][]chan *ComsMessage),
		streams:   make(map[string]*sseWriter),
		stopCh:    make(chan struct{}),
	}
}

// Handler returns the HTTP handler. It is safe to wrap with httptest.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

// Serve serves the hub on ln and blocks until the server is closed.
func (s *Server) Serve(ln net.Listener) error {
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		s.mu.Lock()
		s.boundPort = tcp.Port
		s.mu.Unlock()
	}
	srv := &http.Server{Handler: s.Handler()}
	s.mu.Lock()
	s.httpServer = srv
	s.mu.Unlock()
	err := srv.Serve(ln)
	s.mu.Lock()
	if s.httpServer == srv {
		s.httpServer = nil
	}
	s.mu.Unlock()
	return err
}

// StartLoops starts the stale/offline and TTL sweep loops.
func (s *Server) StartLoops() {
	s.mu.Lock()
	if s.loopsStarted {
		s.mu.Unlock()
		return
	}
	s.loopsStarted = true
	s.loopsWG.Add(2)
	s.mu.Unlock()

	go func() {
		defer s.loopsWG.Done()
		t := time.NewTicker(s.cfg.StaleScanInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.staleScanTick()
			case <-s.stopCh:
				return
			}
		}
	}()
	go func() {
		defer s.loopsWG.Done()
		t := time.NewTicker(s.cfg.TTLScanInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.ttlScanTick()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop closes the shutdown channel, broadcasts shutdown, closes all streams,
// waits for the sweep loops, and closes the HTTP server. It is idempotent.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)

		s.mu.Lock()
		entries := make([]*RegistryEntry, 0, len(s.agents))
		for _, e := range s.agents {
			entries = append(entries, e)
		}
		s.mu.Unlock()
		for _, e := range entries {
			s.broadcast("agent_left", map[string]any{
				"project":    s.cfg.Project,
				"session_id": e.SessionID,
				"name":       e.Name,
				"reason":     "shutdown",
			}, e.SessionID)
		}

		s.mu.Lock()
		for _, wr := range s.streams {
			close(wr.ch)
		}
		s.streams = make(map[string]*sseWriter)
		s.mu.Unlock()

		if s.loopsStarted {
			s.loopsWG.Wait()
		}
		s.mu.Lock()
		hs := s.httpServer
		s.mu.Unlock()
		if hs != nil {
			_ = hs.Close()
		}
	})
}

// BoundPort returns the bound port, or 0 before Serve.
func (s *Server) BoundPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundPort
}

// ServerID returns the hub's ULID.
func (s *Server) ServerID() string { return s.id }

// StartedAt returns the hub's boot time.
func (s *Server) StartedAt() time.Time { return s.startedAt }

// Project returns the hub's project.
func (s *Server) Project() string { return s.cfg.Project }

// IsLoopback reports whether the configured host binds loopback.
func (s *Server) IsLoopback() bool { return IsLoopback(s.cfg.Host) }

// IsLoopback reports whether host is a loopback address.
func IsLoopback(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// ─────────────────────────────────────────────────────────────────────────────
// Routing
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if path == "/health" && method == http.MethodGet {
		s.handleHealth(w, r)
		return
	}
	if !strings.HasPrefix(path, "/v1/") {
		writeError(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if !s.authed(r) {
		s.unauthorized(w)
		return
	}

	switch {
	case path == "/v1/agents/register" && method == http.MethodPost:
		s.handleRegister(w, r)
		return
	case path == "/v1/events" && method == http.MethodGet:
		s.handleEvents(w, r)
		return
	case path == "/v1/agents" && method == http.MethodGet:
		s.handleListAgents(w, r)
		return
	case path == "/v1/messages" && method == http.MethodPost:
		s.handleSendMessage(w, r)
		return
	}

	if rest, ok := strings.CutPrefix(path, "/v1/agents/"); ok {
		parts := strings.Split(rest, "/")
		sid, err := url.PathUnescape(parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_url", nil)
			return
		}
		if len(parts) == 2 && parts[1] == "heartbeat" {
			if method == http.MethodPost {
				s.handleHeartbeat(w, r, sid)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", nil)
			return
		}
		if len(parts) == 1 {
			if method == http.MethodDelete {
				s.handleDeleteAgent(w, r, sid)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", nil)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", nil)
		return
	}

	if rest, ok := strings.CutPrefix(path, "/v1/messages/"); ok {
		parts := strings.Split(rest, "/")
		id, err := url.PathUnescape(parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_url", nil)
			return
		}
		if len(parts) == 2 {
			switch parts[1] {
			case "await":
				if method == http.MethodGet {
					s.handleAwaitMessage(w, r, id)
					return
				}
			case "response":
				if method == http.MethodPost {
					s.handleSubmitResponse(w, r, id)
					return
				}
			default:
				writeError(w, http.StatusNotFound, "not_found", nil)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", nil)
			return
		}
		if len(parts) == 1 {
			if method == http.MethodGet {
				s.handleGetMessage(w, r, id)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", nil)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", nil)
		return
	}

	writeError(w, http.StatusNotFound, "not_found", nil)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"version":    1,
		"server_id":  s.id,
		"started_at": s.startedAt.UTC().Format(time.RFC3339),
	})
}

// resolveProject maps a request project onto the hub's single project. An empty
// project defaults to the hub's project; anything else must match.
func (s *Server) resolveProject(name string) (string, bool) {
	if name == "" || name == s.cfg.Project {
		return s.cfg.Project, true
	}
	return "", false
}

// ─────────────────────────────────────────────────────────────────────────────
// Registry: register, heartbeat, list, delete
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, err := decodeObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", nil)
		return
	}
	sessionID, ok1 := body["session_id"].(string)
	projectStr, ok2 := body["project"].(string)
	nameStr, ok3 := body["name"].(string)
	if !ok1 || !ok2 || !ok3 {
		writeError(w, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	projectName, ok := s.resolveProject(projectStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "project_mismatch", map[string]any{
			"project":        projectStr,
			"server_project": s.cfg.Project,
		})
		return
	}

	desired := nameStr
	if desired == "" {
		desired = "agent"
	}

	s.mu.Lock()
	existing := s.agents[sessionID]
	isReregister := existing != nil

	var resolved string
	if existing != nil {
		if nameStr != existing.Name {
			resolved = s.resolveUniqueNameLocked(desired)
		} else {
			resolved = existing.Name
		}
	} else {
		resolved = s.resolveUniqueNameLocked(desired)
	}

	var provider *string
	if p, ok := body["provider"].(string); ok {
		provider = &p
	}
	purpose, _ := body["purpose"].(string)
	model, _ := body["model"].(string)
	if model == "" {
		model = "unknown"
	}
	color, _ := body["color"].(string)
	if color == "" {
		color = "#888888"
	}
	cwd, _ := body["cwd"].(string)
	explicit := body["explicit"] == true

	startedAt := nowIso()
	if existing != nil {
		startedAt = existing.StartedAt
	}
	card := AgentCard{
		SessionID: sessionID,
		Name:      resolved,
		Purpose:   purpose,
		Model:     model,
		Provider:  provider,
		Color:     color,
		Cwd:       cwd,
		Project:   projectName,
		Explicit:  explicit,
		StartedAt: startedAt,
		Status:    StatusOnline,
	}
	if existing != nil {
		card.ContextUsedPct = existing.ContextUsedPct
		card.QueueDepth = existing.QueueDepth
	}
	registeredAt := nowIso()
	if existing != nil {
		registeredAt = existing.RegisteredAt
	}
	entry := &RegistryEntry{AgentCard: card, LastSeenAt: nowIso(), RegisteredAt: registeredAt}

	if existing != nil && existing.Name != entry.Name {
		s.nameIndexRemove(existing.Name, sessionID)
	}
	s.agents[sessionID] = entry
	s.nameIndexAdd(entry.Name, sessionID)
	card = entry.Card()
	s.mu.Unlock()

	s.logRegister(entry.Name, projectName, sessionID, isReregister)

	s.broadcast("agent_joined", map[string]any{
		"project": projectName,
		"agent":   card,
	}, sessionID)

	sseURL := "/v1/events?project=" + url.QueryEscape(projectName) +
		"&session_id=" + url.QueryEscape(sessionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                    true,
		"agent":                 card,
		"heartbeat_interval_ms": int(s.cfg.HeartbeatInterval / time.Millisecond),
		"sse_url":               sseURL,
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, sessionID string) {
	body, err := decodeObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", nil)
		return
	}
	projectStr, _ := body["project"].(string)
	projectName, ok := s.resolveProject(projectStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "project_mismatch", map[string]any{
			"project":        projectStr,
			"server_project": s.cfg.Project,
		})
		return
	}

	s.mu.Lock()
	entry := s.agents[sessionID]
	if entry == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "agent_not_found", nil)
		return
	}
	beforePct, beforeDepth := entry.ContextUsedPct, entry.QueueDepth
	beforeModel, beforeStatus := entry.Model, entry.Status

	if v, ok := body["context_used_pct"].(float64); ok {
		entry.ContextUsedPct = int(v)
	}
	if v, ok := body["queue_depth"].(float64); ok {
		entry.QueueDepth = int(v)
	}
	if v, ok := body["model"].(string); ok {
		entry.Model = v
	}
	status, _ := body["status"].(string)
	if status == StatusOnline || status == StatusStale || status == StatusOffline {
		entry.Status = status
	} else {
		entry.Status = StatusOnline
	}
	entry.LastSeenAt = nowIso()
	changed := beforePct != entry.ContextUsedPct ||
		beforeDepth != entry.QueueDepth ||
		beforeModel != entry.Model ||
		beforeStatus != entry.Status
	name := entry.Name
	pct, depth := entry.ContextUsedPct, entry.QueueDepth
	var update map[string]any
	if changed {
		update = map[string]any{
			"session_id":       entry.SessionID,
			"name":             entry.Name,
			"context_used_pct": entry.ContextUsedPct,
			"queue_depth":      entry.QueueDepth,
			"model":            entry.Model,
			"status":           entry.Status,
		}
	}
	s.mu.Unlock()

	s.logHeartbeat(name, pct, depth)

	if changed {
		s.broadcast("agent_updated", map[string]any{
			"project": projectName,
			"agent":   update,
		}, sessionID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectName := q.Get("project")
	if projectName == "" {
		projectName = s.cfg.Project
	}
	includeExplicit := q.Get("include_explicit") == "true"

	out := make([]AgentCard, 0)
	if projectName == s.cfg.Project {
		s.mu.Lock()
		for _, e := range s.agents {
			if !includeExplicit && e.Explicit {
				continue
			}
			out = append(out, e.Card())
		}
		s.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request, sessionID string) {
	projectName := r.URL.Query().Get("project")
	if projectName == "" {
		projectName = s.cfg.Project
	}
	if projectName != s.cfg.Project {
		writeError(w, http.StatusNotFound, "agent_not_found", nil)
		return
	}

	s.mu.Lock()
	entry := s.agents[sessionID]
	if entry == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "agent_not_found", nil)
		return
	}
	if wr := s.streams[sessionID]; wr != nil {
		close(wr.ch)
		delete(s.streams, sessionID)
	}
	delete(s.agents, sessionID)
	s.nameIndexRemove(entry.Name, sessionID)
	s.mu.Unlock()

	s.logUnregister(entry.Name, "shutdown")

	s.broadcast("agent_left", map[string]any{
		"project":    projectName,
		"session_id": sessionID,
		"name":       entry.Name,
		"reason":     "shutdown",
	}, sessionID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Messaging: send, get, await, response
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	body, err := decodeObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", nil)
		return
	}
	senderSession, ok1 := body["sender_session"].(string)
	prompt, ok2 := body["prompt"].(string)
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	projectStr, _ := body["project"].(string)
	projectName, ok := s.resolveProject(projectStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "project_mismatch", map[string]any{
			"project":        projectStr,
			"server_project": s.cfg.Project,
		})
		return
	}

	hops := 0
	if v, ok := body["hops"].(float64); ok {
		hops = int(v)
	}

	s.mu.Lock()
	sender := s.agents[senderSession]
	if sender == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "sender_not_registered", nil)
		return
	}
	if hops >= s.cfg.MaxHops {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "hop_limit_exceeded", map[string]any{
			"hops":     hops,
			"max_hops": s.cfg.MaxHops,
		})
		return
	}

	var target *RegistryEntry
	if ts, ok := body["target_session"].(string); ok && ts != "" {
		target = s.agents[ts]
		if target == nil {
			s.mu.Unlock()
			writeError(w, http.StatusNotFound, "target_not_found", nil)
			return
		}
	} else {
		desired, _ := body["target"].(string)
		desired = strings.TrimSpace(desired)
		if desired == "" {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "missing_target", nil)
			return
		}
		if direct := s.agents[desired]; direct != nil {
			target = direct
		} else {
			bag := s.nameIndex[desired]
			if len(bag) == 0 {
				s.mu.Unlock()
				writeError(w, http.StatusNotFound, "target_not_found", map[string]any{"target": desired})
				return
			}
			if len(bag) > 1 {
				candidates := make([]string, 0, len(bag))
				for sid := range bag {
					candidates = append(candidates, sid)
				}
				sort.Strings(candidates)
				s.mu.Unlock()
				writeError(w, http.StatusConflict, "ambiguous_target", map[string]any{
					"target":     desired,
					"candidates": candidates,
				})
				return
			}
			for only := range bag {
				target = s.agents[only]
			}
			if target == nil {
				s.mu.Unlock()
				writeError(w, http.StatusNotFound, "target_not_found", nil)
				return
			}
		}
	}

	depth := s.inboxDepthLocked(target.SessionID)
	if depth >= s.cfg.MaxInbox {
		s.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "inbox_full", map[string]any{
			"depth":     depth,
			"max_inbox": s.cfg.MaxInbox,
		})
		return
	}

	var conversationID any
	if v, ok := body["conversation_id"].(string); ok {
		conversationID = v
	}
	var responseSchema any
	if v, ok := body["response_schema"]; ok && v != nil {
		responseSchema = v
	}

	msg := &ComsMessage{
		MsgID:          ulid(),
		Project:        projectName,
		SenderSession:  senderSession,
		TargetSession:  target.SessionID,
		Prompt:         prompt,
		ConversationID: conversationID,
		ResponseSchema: responseSchema,
		Hops:           hops,
		Status:         MessageQueued,
		CreatedAt:      nowIso(),
		ExpiresAt:      time.Now().UTC().Add(s.cfg.MessageTTL).Format(isoLayout),
	}
	s.messages[msg.MsgID] = msg

	s.sendToStreamLocked(senderSession, "message_status", map[string]any{
		"msg_id": msg.MsgID,
		"status": MessageQueued,
	})

	if s.streams[target.SessionID] != nil {
		s.sendToStreamLocked(target.SessionID, "prompt", map[string]any{
			"msg_id":          msg.MsgID,
			"project":         projectName,
			"sender":          map[string]any{"session_id": sender.SessionID, "name": sender.Name, "cwd": sender.Cwd},
			"prompt":          msg.Prompt,
			"conversation_id": msg.ConversationID,
			"response_schema": msg.ResponseSchema,
			"hops":            msg.Hops,
		})
		msg.Status = MessageDelivered
		msg.DeliveredAt = nowIso()
		s.sendToStreamLocked(senderSession, "message_status", map[string]any{
			"msg_id": msg.MsgID,
			"status": MessageDelivered,
		})
	}
	senderName, targetName, targetSession := sender.Name, target.Name, target.SessionID
	delivered := msg.Status == MessageDelivered
	finalStatus := msg.Status
	s.mu.Unlock()

	s.logMessage(senderName, targetName, msg.MsgID, msg.Prompt, hops, delivered)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"msg_id":         msg.MsgID,
		"status":         finalStatus,
		"target_session": targetSession,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request, msgID string) {
	s.mu.Lock()
	m := s.messages[msgID]
	var v map[string]any
	if m != nil {
		v = m.view()
	}
	s.mu.Unlock()
	if m == nil {
		writeError(w, http.StatusNotFound, "message_not_found", nil)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleAwaitMessage(w http.ResponseWriter, r *http.Request, msgID string) {
	timeout := DefaultAwaitTimeout
	if v := r.URL.Query().Get("timeout_ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Millisecond
		}
	}
	if timeout > s.cfg.MessageTTL {
		timeout = s.cfg.MessageTTL
	}

	ch := make(chan *ComsMessage, 1)
	s.mu.Lock()
	m := s.messages[msgID]
	if m == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "message_not_found", nil)
		return
	}
	if isTerminal(m.Status) {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, m.view())
		return
	}
	s.awaiters[msgID] = append(s.awaiters[msgID], ch)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.removeAwaiterLocked(msgID, ch)
		s.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-r.Context().Done():
		return
	case <-timer.C:
		s.mu.Lock()
		m2 := s.messages[msgID]
		s.mu.Unlock()
		if m2 != nil && isTerminal(m2.Status) {
			writeJSON(w, http.StatusOK, m2.view())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"msg_id":   msgID,
			"status":   MessageTimeout,
			"response": nil,
			"error":    "timeout",
		})
	case final := <-ch:
		writeJSON(w, http.StatusOK, final.view())
	}
}

func (s *Server) handleSubmitResponse(w http.ResponseWriter, r *http.Request, msgID string) {
	body, err := decodeObject(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", nil)
		return
	}
	responderSession, ok := body["responder_session"].(string)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", nil)
		return
	}

	s.mu.Lock()
	m := s.messages[msgID]
	if m == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "message_not_found", nil)
		return
	}
	if responderSession != m.TargetSession {
		s.mu.Unlock()
		writeError(w, http.StatusForbidden, "not_target", nil)
		return
	}
	if isTerminal(m.Status) {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "already_terminal", map[string]any{"status": m.Status})
		return
	}

	errRaw, hasErr := body["error"]
	isError := hasErr && errRaw != nil
	if isError {
		m.Status = MessageError
		e := fmt.Sprint(errRaw)
		m.Error = &e
	} else {
		m.Status = MessageComplete
		m.Error = nil
	}
	m.Response = nil
	if v, ok := body["response"]; ok && v != nil {
		m.Response = v
	}
	m.CompletedAt = nowIso()

	responder := s.agents[responderSession]
	responderName := "unknown"
	if responder != nil {
		responderName = responder.Name
	}

	s.sendToStreamLocked(m.SenderSession, "response", map[string]any{
		"msg_id":    m.MsgID,
		"project":   m.Project,
		"responder": map[string]any{"session_id": responderSession, "name": responderName},
		"response":  m.Response,
		"error":     m.Error,
		"status":    m.Status,
	})
	s.sendToStreamLocked(m.SenderSession, "message_status", map[string]any{
		"msg_id": m.MsgID,
		"status": m.Status,
	})

	s.releaseAwaitersLocked(msgID, m)
	s.mu.Unlock()

	s.logResponse(responderName, m.SenderSession, m.MsgID, isError, m.Error)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectName := q.Get("project")
	if projectName == "" {
		projectName = s.cfg.Project
	}
	sessionID := q.Get("session_id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_session_id", nil)
		return
	}
	if projectName != s.cfg.Project {
		writeError(w, http.StatusNotFound, "agent_not_found", nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", nil)
		return
	}

	s.mu.Lock()
	entry := s.agents[sessionID]
	if entry == nil {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "agent_not_found", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	wr := &sseWriter{ch: make(chan string, 256)}
	if old := s.streams[sessionID]; old != nil {
		close(old.ch)
	}
	s.streams[sessionID] = wr
	s.logSseOpen(entry.Name, len(s.streams))

	wr.lastID++
	wr.ch <- sseFrame("hello", map[string]any{
		"server_time": nowIso(),
		"server_id":   s.id,
	}, wr.lastID)

	agents := make([]AgentCard, 0)
	for _, a := range s.agents {
		if a.SessionID == sessionID {
			continue
		}
		if a.Explicit {
			continue
		}
		agents = append(agents, a.Card())
	}
	wr.lastID++
	wr.ch <- sseFrame("pool_snapshot", map[string]any{
		"project": projectName,
		"agents":  agents,
	}, wr.lastID)
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		keepalive := time.NewTicker(s.cfg.KeepaliveInterval)
		defer keepalive.Stop()
		for {
			select {
			case frame, ok := <-wr.ch:
				if !ok {
					return
				}
				if _, err := io.WriteString(w, frame); err != nil {
					return
				}
				flusher.Flush()
			case <-keepalive.C:
				if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}()

	<-done

	s.mu.Lock()
	if s.streams[sessionID] == wr {
		delete(s.streams, sessionID)
	}
	left := s.agents[sessionID]
	s.mu.Unlock()
	if left != nil {
		s.logSseClose(left.Name, "connection_closed")
		s.broadcast("agent_left", map[string]any{
			"project":    projectName,
			"session_id": sessionID,
			"name":       left.Name,
			"reason":     "connection_closed",
		}, sessionID)
	}
}

// broadcast enqueues an event on every stream except exclude.
func (s *Server) broadcast(event string, data any, exclude string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sid, wr := range s.streams {
		if exclude != "" && sid == exclude {
			continue
		}
		wr.lastID++
		frame := sseFrame(event, data, wr.lastID)
		select {
		case wr.ch <- frame:
		default:
		}
	}
}

// sendToStreamLocked enqueues an event on one stream. Callers hold s.mu.
func (s *Server) sendToStreamLocked(sessionID, event string, data any) {
	wr := s.streams[sessionID]
	if wr == nil {
		return
	}
	wr.lastID++
	frame := sseFrame(event, data, wr.lastID)
	select {
	case wr.ch <- frame:
	default:
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Sweep loops
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) staleScanTick() {
	now := time.Now().UTC()
	type staleItem struct {
		entry *RegistryEntry
	}
	var offlines, stales []staleItem

	s.mu.Lock()
	for sid, entry := range s.agents {
		last, err := parseIso(entry.LastSeenAt)
		if err != nil {
			continue
		}
		dt := now.Sub(last)
		if dt > s.cfg.OfflineAfter {
			delete(s.agents, sid)
			s.nameIndexRemove(entry.Name, sid)
			if wr := s.streams[sid]; wr != nil {
				close(wr.ch)
				delete(s.streams, sid)
			}
			offlines = append(offlines, staleItem{entry})
		} else if dt > s.cfg.StaleAfter && entry.Status != StatusStale {
			entry.Status = StatusStale
			stales = append(stales, staleItem{entry})
		}
	}
	s.mu.Unlock()

	for _, o := range offlines {
		s.logOffline(o.entry.Name)
		s.broadcast("agent_left", map[string]any{
			"project":    s.cfg.Project,
			"session_id": o.entry.SessionID,
			"name":       o.entry.Name,
			"reason":     "stale",
		}, o.entry.SessionID)
	}
	for _, o := range stales {
		s.logStale(o.entry.Name)
		s.broadcast("agent_stale", map[string]any{
			"project":      s.cfg.Project,
			"session_id":   o.entry.SessionID,
			"name":         o.entry.Name,
			"last_seen_at": o.entry.LastSeenAt,
		}, o.entry.SessionID)
	}
}

func (s *Server) ttlScanTick() {
	now := time.Now().UTC()
	type releaseItem struct {
		id string
		m  *ComsMessage
	}
	var releases []releaseItem
	var deletes []string
	var expiredIDs []string

	s.mu.Lock()
	for id, m := range s.messages {
		switch m.Status {
		case MessageQueued, MessageDelivered:
			expires, err := parseIso(m.ExpiresAt)
			if err == nil && now.After(expires) {
				m.Status = MessageError
				e := "expired"
				m.Error = &e
				m.CompletedAt = nowIso()
				releases = append(releases, releaseItem{id, m})
				deletes = append(deletes, id)
				expiredIDs = append(expiredIDs, id)
			}
		case MessageComplete, MessageError:
			completedAt, err := parseIso(m.CompletedAt)
			if err == nil && now.Sub(completedAt) > s.cfg.MessageTTL {
				deletes = append(deletes, id)
			}
		case MessageTimeout:
			expires, err := parseIso(m.ExpiresAt)
			if err == nil && now.After(expires) {
				deletes = append(deletes, id)
			}
		}
	}
	for _, rel := range releases {
		s.releaseAwaitersLocked(rel.id, rel.m)
	}
	for _, id := range deletes {
		delete(s.messages, id)
	}
	s.mu.Unlock()

	for _, id := range expiredIDs {
		s.logExpired(id)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Locked helpers (callers hold s.mu)
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) resolveUniqueNameLocked(desired string) string {
	taken := make(map[string]bool, len(s.agents))
	for _, a := range s.agents {
		taken[a.Name] = true
	}
	if !taken[desired] {
		return desired
	}
	n := 2
	for taken[fmt.Sprintf("%s-%d", desired, n)] {
		n++
	}
	return fmt.Sprintf("%s-%d", desired, n)
}

func (s *Server) nameIndexAdd(name, sessionID string) {
	bag := s.nameIndex[name]
	if bag == nil {
		bag = make(map[string]struct{})
		s.nameIndex[name] = bag
	}
	bag[sessionID] = struct{}{}
}

func (s *Server) nameIndexRemove(name, sessionID string) {
	bag := s.nameIndex[name]
	if bag == nil {
		return
	}
	delete(bag, sessionID)
	if len(bag) == 0 {
		delete(s.nameIndex, name)
	}
}

func (s *Server) inboxDepthLocked(targetSession string) int {
	n := 0
	for _, m := range s.messages {
		if m.TargetSession != targetSession {
			continue
		}
		if m.Status == MessageQueued || m.Status == MessageDelivered {
			n++
		}
	}
	return n
}

func (s *Server) releaseAwaitersLocked(msgID string, m *ComsMessage) {
	for _, ch := range s.awaiters[msgID] {
		select {
		case ch <- m:
		default:
		}
	}
	delete(s.awaiters, msgID)
}

func (s *Server) removeAwaiterLocked(msgID string, ch chan *ComsMessage) {
	set := s.awaiters[msgID]
	for i, c := range set {
		if c == ch {
			set = append(set[:i], set[i+1:]...)
			break
		}
	}
	if len(set) == 0 {
		delete(s.awaiters, msgID)
	} else {
		s.awaiters[msgID] = set
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Logging
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) logEvent(symbol, kind, detail string) {
	if s.cfg.LogQuiet {
		return
	}
	fmt.Printf("%s %s %s %s\n", time.Now().Format("15:04:05.000"), symbol, kind, detail)
}

func (s *Server) logRegister(name, project, sid string, reregister bool) {
	verb := "register"
	if reregister {
		verb = "re-register"
	}
	s.logEvent("✓", verb, fmt.Sprintf("%s@%s sid=%s", name, project, tail6(sid)))
}

func (s *Server) logUnregister(name, reason string) {
	s.logEvent("✗", "unregister", fmt.Sprintf("%s reason=%s", name, reason))
}

func (s *Server) logSseOpen(name string, total int) {
	s.logEvent("⇄", "sse-open", fmt.Sprintf("%s (%d streams)", name, total))
}

func (s *Server) logSseClose(name, reason string) {
	s.logEvent("⇄", "sse-close", fmt.Sprintf("%s reason=%s", name, reason))
}

func (s *Server) logMessage(sender, target, msgID, prompt string, hops int, delivered bool) {
	preview := prompt
	if len(preview) > 50 {
		preview = preview[:47] + "…"
	}
	preview = strings.ReplaceAll(preview, "\n", " ⏎ ")
	status := "queued"
	if delivered {
		status = "delivered"
	}
	s.logEvent("→", "message", fmt.Sprintf("%s → %s %s %q hops=%d %s", sender, target, tail6(msgID), preview, hops, status))
}

func (s *Server) logResponse(responder, sender, msgID string, isError bool, err *string) {
	detail := "ok"
	if isError {
		detail = "error=" + stringOr(err, "")
	}
	s.logEvent("←", "response", fmt.Sprintf("%s → %s %s %s", responder, sender, tail6(msgID), detail))
}

func (s *Server) logStale(name string) {
	s.logEvent("⚠", "stale", name)
}

func (s *Server) logOffline(name string) {
	s.logEvent("⌛", "offline", fmt.Sprintf("%s removed (no heartbeat)", name))
}

func (s *Server) logExpired(msgID string) {
	s.logEvent("⏱", "expired", tail6(msgID))
}

func (s *Server) logHeartbeat(name string, pct, depth int) {
	if !s.cfg.LogHeartbeat {
		return
	}
	s.logEvent("♥", "heartbeat", fmt.Sprintf("%s ctx=%d%% queue=%d", name, pct, depth))
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire helpers
// ─────────────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, errorStr string, details any) {
	body := map[string]any{"ok": false, "error": errorStr}
	if details != nil {
		body["details"] = details
	}
	writeJSON(w, status, body)
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="comms"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"ok":false,"error":"unauthorized"}`)
}

func (s *Server) authed(r *http.Request) bool {
	if s.cfg.Token == "" {
		return false
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len("Bearer "):]), []byte(s.cfg.Token)) == 1
}

func decodeObject(r *http.Request) (map[string]any, error) {
	var raw any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return m, nil
}

func sseFrame(event string, data any, id int64) string {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte("null")
	}
	if id > 0 {
		return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", event, id, b)
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, b)
}

func isTerminal(status string) bool {
	return status == MessageComplete || status == MessageError || status == MessageTimeout
}

func nowIso() string {
	return time.Now().UTC().Format(isoLayout)
}

func parseIso(s string) (time.Time, error) {
	return time.Parse(isoLayout, s)
}

func tail6(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func stringOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// ulid returns a 26-character Crockford-Base32 ULID: a 48-bit millisecond
// timestamp followed by 80 bits from crypto/rand.
func ulid() string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	var raw [16]byte
	ms := uint64(time.Now().UnixMilli())
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		panic(fmt.Sprintf("ulid: crypto/rand: %v", err))
	}

	var out [26]byte
	for i := 0; i < len(out); i++ {
		bitOffset := 5 * i
		byteIdx := bitOffset / 8
		bitInByte := bitOffset % 8
		var v uint16 = uint16(raw[byteIdx]) << 8
		if byteIdx+1 < len(raw) {
			v |= uint16(raw[byteIdx+1])
		}
		shift := uint(15 - bitInByte - 4)
		out[i] = alphabet[(v>>shift)&31]
	}
	return string(out[:])
}
