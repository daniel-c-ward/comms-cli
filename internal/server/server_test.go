package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"comms-cli/internal/server"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test environment
// ─────────────────────────────────────────────────────────────────────────────

type testEnv struct {
	s     *server.Server
	base  string
	token string
}

func newEnv(t *testing.T, mutate func(*server.Config)) *testEnv {
	t.Helper()
	cfg := server.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Token = "test-token"
	cfg.Project = "proj"
	if mutate != nil {
		mutate(&cfg)
	}
	s := server.NewServer(cfg)
	s.StartLoops()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go s.Serve(ln)
	t.Cleanup(func() { s.Stop() })
	port := ln.Addr().(*net.TCPAddr).Port
	return &testEnv{s: s, base: fmt.Sprintf("http://127.0.0.1:%d", port), token: cfg.Token}
}

func (e *testEnv) doRaw(method, path string, body any, token string) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func (e *testEnv) do(t *testing.T, method, path string, body any, token string) (int, []byte) {
	t.Helper()
	status, data, err := e.doRaw(method, path, body, token)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return status, data
}

func decodeMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return m
}

func register(t *testing.T, e *testEnv, session, name string) map[string]any {
	t.Helper()
	return registerFull(t, e, session, name, false)
}

func registerFull(t *testing.T, e *testEnv, session, name string, explicit bool) map[string]any {
	t.Helper()
	status, data := e.do(t, http.MethodPost, "/v1/agents/register", map[string]any{
		"project":    "proj",
		"session_id": session,
		"name":       name,
		"purpose":    "purpose of " + name,
		"model":      "test-model",
		"color":      "#abcdef",
		"cwd":        "/tmp",
		"explicit":   explicit,
	}, e.token)
	if status != http.StatusOK {
		t.Fatalf("register %s: status %d body %s", name, status, data)
	}
	return decodeMap(t, data)
}

func heartbeat(t *testing.T, e *testEnv, session string, fields map[string]any) int {
	t.Helper()
	body := map[string]any{"project": "proj"}
	for k, v := range fields {
		body[k] = v
	}
	status, data := e.do(t, http.MethodPost, "/v1/agents/"+session+"/heartbeat", body, e.token)
	if status == http.StatusOK {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("heartbeat decode: %v", err)
		}
		if m["ok"] != true {
			t.Fatalf("heartbeat ok=false: %s", data)
		}
	}
	return status
}

func sendMsg(t *testing.T, e *testEnv, fields map[string]any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"project": "proj", "prompt": "hello"}
	for k, v := range fields {
		body[k] = v
	}
	status, data := e.do(t, http.MethodPost, "/v1/messages", body, e.token)
	if status == http.StatusOK {
		return status, decodeMap(t, data)
	}
	return status, decodeMap(t, data)
}

func submitResponse(t *testing.T, e *testEnv, msgID, responder string, payload map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{"project": "proj", "responder_session": responder}
	for k, v := range payload {
		body[k] = v
	}
	return e.do(t, http.MethodPost, "/v1/messages/"+msgID+"/response", body, e.token)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE helpers
// ─────────────────────────────────────────────────────────────────────────────

type stream struct {
	resp   *http.Response
	sc     *bufio.Scanner
	cancel context.CancelFunc
}

func openStream(t *testing.T, e *testEnv, session string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		e.base+"/v1/events?project=proj&session_id="+session, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("open stream: status %d", resp.StatusCode)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return &stream{resp: resp, sc: bufio.NewScanner(resp.Body), cancel: cancel}
}

func (st *stream) nextFrame() ([]string, error) {
	var lines []string
	for st.sc.Scan() {
		line := st.sc.Text()
		if line == "" {
			return lines, nil
		}
		lines = append(lines, line)
	}
	if err := st.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (st *stream) readWithTimeout(d time.Duration) ([]string, error) {
	type res struct {
		lines []string
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := st.nextFrame()
		ch <- res{l, err}
	}()
	select {
	case r := <-ch:
		return r.lines, r.err
	case <-time.After(d):
		st.cancel()
		return nil, fmt.Errorf("timed out after %s", d)
	}
}

func parseFrame(lines []string) (event, id, data string) {
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(l, "id: "); ok {
			id = v
		}
		if v, ok := strings.CutPrefix(l, "data: "); ok {
			data = v
		}
	}
	return
}

func mustReadEvent(t *testing.T, st *stream, d time.Duration, wantEvent string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("no %s frame within deadline", wantEvent)
		}
		lines, err := st.readWithTimeout(remain)
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if len(lines) == 1 && strings.HasPrefix(lines[0], ":") {
			continue // keepalive comment
		}
		event, _, data := parseFrame(lines)
		if event == wantEvent {
			var m map[string]any
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				t.Fatalf("bad %s data %q: %v", wantEvent, data, err)
			}
			return m
		}
	}
}

func mustReadComment(t *testing.T, st *stream, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("no keepalive comment within deadline")
		}
		lines, err := st.readWithTimeout(remain)
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if len(lines) == 1 && strings.HasPrefix(lines[0], ":") {
			return
		}
	}
}

func mustReadFrame(t *testing.T, st *stream, d time.Duration) []string {
	t.Helper()
	lines, err := st.readWithTimeout(d)
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	return lines
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 02 — register, health, auth
// ─────────────────────────────────────────────────────────────────────────────

func TestHealthUnauthenticated(t *testing.T) {
	e := newEnv(t, nil)
	status, data := e.do(t, http.MethodGet, "/health", nil, "")
	if status != http.StatusOK {
		t.Fatalf("health status %d", status)
	}
	m := decodeMap(t, data)
	if m["ok"] != true || m["version"] != float64(1) {
		t.Fatalf("health shape: %v", m)
	}
	if _, ok := m["server_id"].(string); !ok {
		t.Fatalf("health server_id missing: %v", m)
	}
	if _, ok := m["started_at"].(string); !ok {
		t.Fatalf("health started_at missing: %v", m)
	}
}

func TestRegisterSuccess(t *testing.T) {
	e := newEnv(t, nil)
	m := register(t, e, "sess-a", "alice")

	agent, ok := m["agent"].(map[string]any)
	if !ok {
		t.Fatalf("register response missing agent: %v", m)
	}
	if m["ok"] != true {
		t.Fatalf("ok != true: %v", m)
	}
	if agent["session_id"] != "sess-a" || agent["name"] != "alice" {
		t.Fatalf("bad agent identity: %v", agent)
	}
	if agent["status"] != "online" || agent["project"] != "proj" {
		t.Fatalf("bad agent card fields: %v", agent)
	}
	for _, key := range []string{"session_id", "name", "purpose", "model", "color", "cwd", "project", "explicit", "started_at", "context_used_pct", "queue_depth", "status"} {
		if _, ok := agent[key]; !ok {
			t.Fatalf("agent card missing %q: %v", key, agent)
		}
	}
	if m["heartbeat_interval_ms"] != float64(10000) {
		t.Fatalf("heartbeat_interval_ms: %v", m["heartbeat_interval_ms"])
	}
	sseURL, _ := m["sse_url"].(string)
	if !strings.Contains(sseURL, "session_id=sess-a") || !strings.Contains(sseURL, "project=proj") {
		t.Fatalf("bad sse_url: %q", sseURL)
	}
}

func TestRegisterNameCollision(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	m2 := register(t, e, "sess-b", "alice")
	agent := m2["agent"].(map[string]any)
	if agent["name"] != "alice-2" {
		t.Fatalf("collision suffix wrong: %v", agent["name"])
	}
}

func TestRegisterUpsertKeepsName(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "alice")
	// Re-register same session, same name: keep "alice".
	m := register(t, e, "sess-a", "alice")
	agent := m["agent"].(map[string]any)
	if agent["name"] != "alice" {
		t.Fatalf("upsert should keep name, got %v", agent["name"])
	}
	// Re-register same session with a new name: resolve fresh.
	m = register(t, e, "sess-a", "carol")
	agent = m["agent"].(map[string]any)
	if agent["name"] != "carol" {
		t.Fatalf("rename should resolve carol, got %v", agent["name"])
	}
}

func TestRegisterMissingFields(t *testing.T) {
	e := newEnv(t, nil)
	cases := []map[string]any{
		{"project": "proj", "name": "alice"},                    // no session_id
		{"project": "proj", "session_id": "sess-a"},             // no name
		{"session_id": "sess-a", "name": "alice"},               // no project
		{"project": 1, "session_id": "sess-a", "name": "alice"}, // project not string
		{"project": "proj", "session_id": 1, "name": "alice"},   // session not string
	}
	for i, body := range cases {
		status, data := e.do(t, http.MethodPost, "/v1/agents/register", body, e.token)
		if status != http.StatusBadRequest {
			t.Fatalf("case %d: status %d body %s", i, status, data)
		}
		m := decodeMap(t, data)
		if m["error"] != "invalid_request" {
			t.Fatalf("case %d: error %v", i, m["error"])
		}
	}
}

func TestRegisterProjectMismatch(t *testing.T) {
	e := newEnv(t, nil)
	status, data := e.do(t, http.MethodPost, "/v1/agents/register", map[string]any{
		"project": "other", "session_id": "sess-a", "name": "alice",
	}, e.token)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d", status)
	}
	m := decodeMap(t, data)
	if m["error"] != "project_mismatch" {
		t.Fatalf("error %v", m["error"])
	}
}

func TestV1RequiresAuth(t *testing.T) {
	e := newEnv(t, nil)
	// Missing token.
	status, data := e.do(t, http.MethodGet, "/v1/agents", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("missing token: status %d", status)
	}
	assertUnauthorizedBody(t, data)
	// Malformed header.
	status, data = e.do(t, http.MethodGet, "/v1/agents", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("malformed: status %d", status)
	}
	req, _ := http.NewRequest(http.MethodGet, e.base+"/v1/agents", nil)
	req.Header.Set("Authorization", "Token abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("malformed token: status %d", resp.StatusCode)
	}
	// Wrong token.
	status, data = e.do(t, http.MethodGet, "/v1/agents", nil, "wrong-token")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d", status)
	}
	assertUnauthorizedBody(t, data)
}

func assertUnauthorizedBody(t *testing.T, data []byte) {
	t.Helper()
	m := decodeMap(t, data)
	if m["ok"] != false || m["error"] != "unauthorized" {
		t.Fatalf("unauthorized body: %s", data)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 03 — SSE backbone
// ─────────────────────────────────────────────────────────────────────────────

func TestSseHelloFrame(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")

	lines := mustReadFrame(t, st, 2*time.Second)
	event, id, data := parseFrame(lines)
	if event != "hello" {
		t.Fatalf("first frame event %q", event)
	}
	if id == "" {
		t.Fatalf("hello frame missing id: %v", lines)
	}
	m := decodeMap(t, []byte(data))
	if _, ok := m["server_time"].(string); !ok {
		t.Fatalf("hello missing server_time: %v", m)
	}
	if m["server_id"] != e.s.ServerID() {
		t.Fatalf("hello server_id mismatch: %v", m["server_id"])
	}
}

func TestSseKeepaliveComment(t *testing.T) {
	e := newEnv(t, func(c *server.Config) { c.KeepaliveInterval = 20 * time.Millisecond })
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")
	mustReadComment(t, st, 3*time.Second)
}

func TestSseUnregisteredRejected(t *testing.T) {
	e := newEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		e.base+"/v1/events?project=proj&session_id=ghost", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	m := decodeMap(t, data)
	if m["error"] != "agent_not_found" {
		t.Fatalf("error %v", m["error"])
	}
}

func TestSseMonotonicIds(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")

	lines := mustReadFrame(t, st, 2*time.Second)
	_, id1, _ := parseFrame(lines)
	lines = mustReadFrame(t, st, 2*time.Second)
	event2, id2, _ := parseFrame(lines)
	if event2 != "pool_snapshot" {
		t.Fatalf("second frame event %q", event2)
	}

	register(t, e, "sess-b", "bob")
	lines = mustReadFrame(t, st, 2*time.Second)
	event3, id3, data3 := parseFrame(lines)
	if event3 != "agent_joined" {
		t.Fatalf("third frame event %q", event3)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data3), &m); err != nil {
		t.Fatalf("bad agent_joined data %q: %v", data3, err)
	}
	if m["agent"].(map[string]any)["name"] != "bob" {
		t.Fatalf("agent_joined payload: %v", m)
	}

	if id1 != "1" || id2 != "2" || id3 != "3" {
		t.Fatalf("ids not monotonic per-writer: %q %q %q", id1, id2, id3)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 04 — events, heartbeat, agent list
// ─────────────────────────────────────────────────────────────────────────────

func TestPoolSnapshotOnConnect(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	st := openStream(t, e, "sess-b")

	lines := mustReadFrame(t, st, 2*time.Second) // hello
	lines = mustReadFrame(t, st, 2*time.Second)
	event, _, data := parseFrame(lines)
	if event != "pool_snapshot" {
		t.Fatalf("expected pool_snapshot, got %q (%v)", event, lines)
	}
	m := decodeMap(t, []byte(data))
	agents, ok := m["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("snapshot agents: %v", m["agents"])
	}
	card := agents[0].(map[string]any)
	if card["name"] != "alice" {
		t.Fatalf("snapshot card: %v", card)
	}
}

func TestPoolSnapshotExcludesSelfAndIsArray(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")

	lines := mustReadFrame(t, st, 2*time.Second) // hello
	lines = mustReadFrame(t, st, 2*time.Second)
	_, _, data := parseFrame(lines)
	if !strings.Contains(data, `"agents":[]`) {
		t.Fatalf("empty snapshot should be [] not null: %s", data)
	}
}

func TestAgentJoinedBroadcast(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")

	register(t, e, "sess-b", "bob")
	m := mustReadEvent(t, st, 2*time.Second, "agent_joined")
	if m["project"] != "proj" {
		t.Fatalf("agent_joined project: %v", m)
	}
	agent := m["agent"].(map[string]any)
	if agent["name"] != "bob" || agent["session_id"] != "sess-b" {
		t.Fatalf("agent_joined agent: %v", agent)
	}
}

func TestHeartbeatUpdatesCard(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	status := heartbeat(t, e, "sess-a", map[string]any{
		"context_used_pct": float64(55),
		"queue_depth":      float64(2),
		"model":            "gpt-x",
		"status":           "online",
	})
	if status != http.StatusOK {
		t.Fatalf("heartbeat status %d", status)
	}
	_, data := e.do(t, http.MethodGet, "/v1/agents", nil, e.token)
	m := decodeMap(t, data)
	agents := m["agents"].([]any)
	card := agents[0].(map[string]any)
	if card["name"] != "alice" {
		t.Fatalf("card: %v", card)
	}
	if card["context_used_pct"] != float64(55) || card["queue_depth"] != float64(2) || card["model"] != "gpt-x" {
		t.Fatalf("card not updated: %v", card)
	}
	if card["status"] != "online" {
		t.Fatalf("status: %v", card["status"])
	}
}

func TestHeartbeatInvalidStatusResetsOnline(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	heartbeat(t, e, "sess-a", map[string]any{"status": "banana"})
	_, data := e.do(t, http.MethodGet, "/v1/agents", nil, e.token)
	card := decodeMap(t, data)["agents"].([]any)[0].(map[string]any)
	if card["status"] != "online" {
		t.Fatalf("invalid status should reset to online, got %v", card["status"])
	}
}

func TestAgentUpdatedBroadcast(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")
	register(t, e, "sess-b", "bob")
	mustReadEvent(t, st, 2*time.Second, "agent_joined")

	heartbeat(t, e, "sess-b", map[string]any{"context_used_pct": float64(42)})
	m := mustReadEvent(t, st, 2*time.Second, "agent_updated")
	agent := m["agent"].(map[string]any)
	for _, key := range []string{"session_id", "name", "context_used_pct", "queue_depth", "model", "status"} {
		if _, ok := agent[key]; !ok {
			t.Fatalf("agent_updated missing %q: %v", key, agent)
		}
	}
	if agent["session_id"] != "sess-b" || agent["context_used_pct"] != float64(42) {
		t.Fatalf("agent_updated payload: %v", agent)
	}
}

func TestListAgentsFiltersExplicit(t *testing.T) {
	e := newEnv(t, nil)
	registerFull(t, e, "sess-a", "alice", false)
	registerFull(t, e, "sess-b", "bob", true)

	_, data := e.do(t, http.MethodGet, "/v1/agents", nil, e.token)
	agents := decodeMap(t, data)["agents"].([]any)
	if len(agents) != 1 || agents[0].(map[string]any)["name"] != "alice" {
		t.Fatalf("explicit not filtered: %v", agents)
	}

	_, data = e.do(t, http.MethodGet, "/v1/agents?include_explicit=true", nil, e.token)
	agents = decodeMap(t, data)["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("include_explicit should list both: %v", agents)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 05 — send and get
// ─────────────────────────────────────────────────────────────────────────────

func TestSendByNameDeliversToStream(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	st := openStream(t, e, "sess-b")

	status, m := sendMsg(t, e, map[string]any{
		"sender_session": "sess-a",
		"target":         "bob",
		"prompt":         "hi there",
	})
	if status != http.StatusOK {
		t.Fatalf("send status %d body %v", status, m)
	}
	if m["ok"] != true || m["status"] != "delivered" || m["target_session"] != "sess-b" {
		t.Fatalf("send response: %v", m)
	}
	msgID, _ := m["msg_id"].(string)
	if msgID == "" {
		t.Fatalf("missing msg_id: %v", m)
	}

	ev := mustReadEvent(t, st, 2*time.Second, "prompt")
	if ev["msg_id"] != msgID || ev["prompt"] != "hi there" || ev["hops"] != float64(0) {
		t.Fatalf("prompt payload: %v", ev)
	}
	sender := ev["sender"].(map[string]any)
	if sender["session_id"] != "sess-a" || sender["name"] != "alice" {
		t.Fatalf("prompt sender: %v", sender)
	}
	if ev["conversation_id"] != nil || ev["response_schema"] != nil {
		t.Fatalf("null fields not null: %v", ev)
	}

	_, data := e.do(t, http.MethodGet, "/v1/messages/"+msgID, nil, e.token)
	got := decodeMap(t, data)
	if got["status"] != "delivered" || got["response"] != nil || got["error"] != nil {
		t.Fatalf("message view: %v", got)
	}
}

func TestSendQueuedWhenTargetOffline(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	if status != http.StatusOK || m["status"] != "queued" {
		t.Fatalf("expected queued, got %d %v", status, m)
	}
}

func TestSendBySessionID(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	status, m := sendMsg(t, e, map[string]any{
		"sender_session": "sess-a",
		"target_session": "sess-b",
		"prompt":         "direct",
	})
	if status != http.StatusOK || m["target_session"] != "sess-b" {
		t.Fatalf("send by session: %d %v", status, m)
	}
}

func TestSendHopLimitExceeded(t *testing.T) {
	e := newEnv(t, func(c *server.Config) { c.MaxHops = 5 })
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	status, m := sendMsg(t, e, map[string]any{
		"sender_session": "sess-a", "target": "bob", "prompt": "x", "hops": float64(5),
	})
	if status != http.StatusConflict || m["error"] != "hop_limit_exceeded" {
		t.Fatalf("hop limit: %d %v", status, m)
	}
	if m["details"].(map[string]any)["max_hops"] != float64(5) {
		t.Fatalf("details: %v", m["details"])
	}
	status, m = sendMsg(t, e, map[string]any{
		"sender_session": "sess-a", "target": "bob", "prompt": "x", "hops": float64(4),
	})
	if status != http.StatusOK {
		t.Fatalf("hops under limit should pass: %d %v", status, m)
	}
}

func TestSendInboxFull(t *testing.T) {
	e := newEnv(t, func(c *server.Config) { c.MaxInbox = 2 })
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	for i := 0; i < 2; i++ {
		status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "m"})
		if status != http.StatusOK {
			t.Fatalf("send %d: %d %v", i, status, m)
		}
	}
	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "m"})
	if status != http.StatusTooManyRequests || m["error"] != "inbox_full" {
		t.Fatalf("inbox full: %d %v", status, m)
	}
	d := m["details"].(map[string]any)
	if d["depth"] != float64(2) || d["max_inbox"] != float64(2) {
		t.Fatalf("details: %v", d)
	}
}

func TestSendUnknownTarget(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "nobody", "prompt": "x"})
	if status != http.StatusNotFound || m["error"] != "target_not_found" {
		t.Fatalf("unknown target: %d %v", status, m)
	}
}

func TestSendMissingTarget(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "prompt": "x"})
	if status != http.StatusBadRequest || m["error"] != "missing_target" {
		t.Fatalf("missing target: %d %v", status, m)
	}
}

func TestSendSenderNotRegistered(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-b", "bob")
	status, m := sendMsg(t, e, map[string]any{"sender_session": "ghost", "target": "bob", "prompt": "x"})
	if status != http.StatusNotFound || m["error"] != "sender_not_registered" {
		t.Fatalf("sender not registered: %d %v", status, m)
	}
}

func TestGetMessage404(t *testing.T) {
	e := newEnv(t, nil)
	status, data := e.do(t, http.MethodGet, "/v1/messages/nonexistent", nil, e.token)
	if status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
	if decodeMap(t, data)["error"] != "message_not_found" {
		t.Fatalf("body %s", data)
	}
}

func TestMessageStatusEventsToSender(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")
	register(t, e, "sess-b", "bob")
	stBob := openStream(t, e, "sess-b")

	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	if status != http.StatusOK || m["status"] != "delivered" {
		t.Fatalf("send: %d %v", status, m)
	}
	q := mustReadEvent(t, st, 2*time.Second, "message_status")
	if q["status"] != "queued" {
		t.Fatalf("first status: %v", q)
	}
	d := mustReadEvent(t, st, 2*time.Second, "message_status")
	if d["status"] != "delivered" {
		t.Fatalf("second status: %v", d)
	}
	_ = stBob
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 06 — await and response
// ─────────────────────────────────────────────────────────────────────────────

func TestAwaitTerminalImmediate(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	status, _ := submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "ok"})
	if status != http.StatusOK {
		t.Fatalf("response status %d", status)
	}

	start := time.Now()
	status, data := e.do(t, http.MethodGet, "/v1/messages/"+msgID+"/await", nil, e.token)
	if status != http.StatusOK {
		t.Fatalf("await status %d", status)
	}
	got := decodeMap(t, data)
	if got["status"] != "complete" || got["response"] != "ok" {
		t.Fatalf("await view: %v", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("await on terminal message should be immediate")
	}
}

func TestAwaitRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "question"})
	msgID := m["msg_id"].(string)

	type res struct {
		status int
		data   []byte
		err    error
	}
	ch := make(chan res, 1)
	go func() {
		s, d, err := e.doRaw(http.MethodGet, "/v1/messages/"+msgID+"/await?timeout_ms=5000", nil, e.token)
		ch <- res{s, d, err}
	}()
	time.Sleep(100 * time.Millisecond)

	status, _ := submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "the answer"})
	if status != http.StatusOK {
		t.Fatalf("response status %d", status)
	}
	r := <-ch
	if r.err != nil || r.status != http.StatusOK {
		t.Fatalf("await: %v %d", r.err, r.status)
	}
	got := decodeMap(t, r.data)
	if got["status"] != "complete" || got["response"] != "the answer" {
		t.Fatalf("await round-trip: %v", got)
	}
}

func TestAwaitErrorPath(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	status, _ := submitResponse(t, e, msgID, "sess-b", map[string]any{"error": "nope"})
	if status != http.StatusOK {
		t.Fatalf("response status %d", status)
	}
	status, data := e.do(t, http.MethodGet, "/v1/messages/"+msgID+"/await", nil, e.token)
	got := decodeMap(t, data)
	if got["status"] != "error" || got["error"] != "nope" {
		t.Fatalf("await error: %v", got)
	}
}

func TestAwaitTimeout(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	start := time.Now()
	status, data := e.do(t, http.MethodGet, "/v1/messages/"+msgID+"/await?timeout_ms=80", nil, e.token)
	if status != http.StatusOK {
		t.Fatalf("await status %d", status)
	}
	got := decodeMap(t, data)
	if got["status"] != "timeout" || got["error"] != "timeout" {
		t.Fatalf("await timeout view: %v", got)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestResponseSpoofedResponder(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	register(t, e, "sess-c", "charlie")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	status, data := submitResponse(t, e, msgID, "sess-c", map[string]any{"response": "spoof"})
	if status != http.StatusForbidden {
		t.Fatalf("spoofed responder: %d %s", status, data)
	}
	if decodeMap(t, data)["error"] != "not_target" {
		t.Fatalf("body %s", data)
	}
}

func TestResponseAlreadyTerminal(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "first"})
	status, data := submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "second"})
	if status != http.StatusConflict {
		t.Fatalf("already terminal: %d %s", status, data)
	}
	if decodeMap(t, data)["error"] != "already_terminal" {
		t.Fatalf("body %s", data)
	}
}

func TestResponseEventsToSender(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")
	register(t, e, "sess-b", "bob")
	bobSt := openStream(t, e, "sess-b")

	status, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	if status != http.StatusOK {
		t.Fatalf("send: %d %v", status, m)
	}
	msgID := m["msg_id"].(string)
	mustReadEvent(t, st, 2*time.Second, "message_status") // queued
	mustReadEvent(t, st, 2*time.Second, "message_status") // delivered
	mustReadEvent(t, bobSt, 2*time.Second, "prompt")

	submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "done"})
	r := mustReadEvent(t, st, 2*time.Second, "response")
	if r["msg_id"] != msgID || r["status"] != "complete" || r["response"] != "done" {
		t.Fatalf("response event: %v", r)
	}
	if r["responder"].(map[string]any)["name"] != "bob" {
		t.Fatalf("responder: %v", r["responder"])
	}
	s := mustReadEvent(t, st, 2*time.Second, "message_status")
	if s["status"] != "complete" {
		t.Fatalf("final status: %v", s)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 07 — delete agent + stale/offline
// ─────────────────────────────────────────────────────────────────────────────

func TestDeleteAgentBroadcastsLeft(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	st := openStream(t, e, "sess-b")

	status, data := e.do(t, http.MethodDelete, "/v1/agents/sess-a?project=proj", nil, e.token)
	if status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, data)
	}
	m := mustReadEvent(t, st, 2*time.Second, "agent_left")
	if m["session_id"] != "sess-a" || m["reason"] != "shutdown" {
		t.Fatalf("agent_left: %v", m)
	}
	_, data = e.do(t, http.MethodGet, "/v1/agents", nil, e.token)
	if len(decodeMap(t, data)["agents"].([]any)) != 1 {
		t.Fatalf("alice should be gone: %s", data)
	}
}

func TestDeleteUnknownAgent(t *testing.T) {
	e := newEnv(t, nil)
	status, _ := e.do(t, http.MethodDelete, "/v1/agents/ghost?project=proj", nil, e.token)
	if status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
}

func TestStreamCloseBroadcastsLeft(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-b", "bob")
	st := openStream(t, e, "sess-b")

	register(t, e, "sess-a", "alice")
	mustReadEvent(t, st, 2*time.Second, "agent_joined")

	stAlice := openStream(t, e, "sess-a")
	stAlice.cancel()
	stAlice.resp.Body.Close()
	m := mustReadEvent(t, st, 3*time.Second, "agent_left")
	if m["session_id"] != "sess-a" || m["reason"] != "connection_closed" {
		t.Fatalf("agent_left on close: %v", m)
	}
}

func TestStaleThenOfflineSweep(t *testing.T) {
	e := newEnv(t, func(c *server.Config) {
		c.StaleAfter = 50 * time.Millisecond
		c.OfflineAfter = 120 * time.Millisecond
		c.StaleScanInterval = 20 * time.Millisecond
	})
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			_, _, _ = e.doRaw(http.MethodPost, "/v1/agents/sess-b/heartbeat", map[string]any{"project": "proj"}, e.token)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	defer close(done)

	st := openStream(t, e, "sess-b")

	m := mustReadEvent(t, st, 5*time.Second, "agent_stale")
	if m["session_id"] != "sess-a" {
		t.Fatalf("agent_stale: %v", m)
	}

	m = mustReadEvent(t, st, 5*time.Second, "agent_left")
	if m["session_id"] != "sess-a" || m["reason"] != "stale" {
		t.Fatalf("offline agent_left: %v", m)
	}

	_, data := e.do(t, http.MethodGet, "/v1/agents", nil, e.token)
	agents := decodeMap(t, data)["agents"].([]any)
	if len(agents) != 1 || agents[0].(map[string]any)["name"] != "bob" {
		t.Fatalf("alice should be reaped: %v", agents)
	}
}

func TestReregisterAfterRemoval(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	e.do(t, http.MethodDelete, "/v1/agents/sess-a?project=proj", nil, e.token)
	m := register(t, e, "sess-a", "alice")
	agent := m["agent"].(map[string]any)
	if agent["name"] != "alice" {
		t.Fatalf("fresh register should reclaim name, got %v", agent["name"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ticket 08 — TTL sweep + graceful shutdown
// ─────────────────────────────────────────────────────────────────────────────

func TestExpiredMessageTerminalAndReleasesAwaiters(t *testing.T) {
	e := newEnv(t, func(c *server.Config) {
		c.MessageTTL = 60 * time.Millisecond
		c.TTLScanInterval = 20 * time.Millisecond
	})
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)

	// The await timeout is clamped to the TTL, so it resolves with a terminal
	// status at ~TTL: either the sweep's "error/expired" or the awaiter's own
	// "timeout", whichever fires first.
	start := time.Now()
	status, data := e.do(t, http.MethodGet, "/v1/messages/"+msgID+"/await?timeout_ms=5000", nil, e.token)
	if status != http.StatusOK {
		t.Fatalf("await status %d", status)
	}
	got := decodeMap(t, data)
	terminal := got["status"] == "timeout" ||
		(got["status"] == "error" && got["error"] == "expired")
	if !terminal {
		t.Fatalf("await should be terminal on expiry, got: %v", got)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("await released too late")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _ = e.do(t, http.MethodGet, "/v1/messages/"+msgID, nil, e.token)
		if status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expired message not swept within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTerminalMessageTTLCleanup(t *testing.T) {
	e := newEnv(t, func(c *server.Config) {
		c.MessageTTL = 60 * time.Millisecond
		c.TTLScanInterval = 20 * time.Millisecond
	})
	register(t, e, "sess-a", "alice")
	register(t, e, "sess-b", "bob")
	_, m := sendMsg(t, e, map[string]any{"sender_session": "sess-a", "target": "bob", "prompt": "hi"})
	msgID := m["msg_id"].(string)
	submitResponse(t, e, msgID, "sess-b", map[string]any{"response": "done"})

	status, _ := e.do(t, http.MethodGet, "/v1/messages/"+msgID, nil, e.token)
	if status != http.StatusOK {
		t.Fatalf("message should exist before TTL")
	}
	time.Sleep(150 * time.Millisecond)
	status, _ = e.do(t, http.MethodGet, "/v1/messages/"+msgID, nil, e.token)
	if status != http.StatusNotFound {
		t.Fatalf("terminal message should be swept after TTL")
	}
}

func TestStopClosesStreams(t *testing.T) {
	e := newEnv(t, nil)
	register(t, e, "sess-a", "alice")
	st := openStream(t, e, "sess-a")
	mustReadFrame(t, st, 2*time.Second) // hello

	e.s.Stop()
	mustClose(t, st, 3*time.Second)
}

func mustClose(t *testing.T, st *stream, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("stream still open after %s", d)
		}
		_, err := st.readWithTimeout(remain)
		if err != nil {
			return
		}
	}
}

func TestStopIdempotent(t *testing.T) {
	e := newEnv(t, nil)
	e.s.Stop()
	e.s.Stop() // must not panic or hang
}
