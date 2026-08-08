# comms-cli — feature landscape research

**Date:** 2026-08-08
**Scope:** Two background research passes against primary sources (official docs, spec repos,
first-party sites). Thread A surveyed the multi-agent hub landscape for capability gaps; Thread B
established the MCP facts needed for the planned comms MCP server. Neither pass touched code.

**Sources are cited inline.** Thread B source list is at the end; Thread A's full appendix was
merged into its inline citations.

---

## Executive summary

Two findings dominate:

1. **The planned MCP server cannot push inbound work.** As of August 2026, core MCP has **no**
   server-initiated work delivery (SEP-2260, Final, forbids it). The comms MCP server must use
   **client polling** — a blocking `await_message` / `get_next_prompt` long-poll tool — not
   notifications. The one exception is Claude Code "Channels", a vendor-specific research preview
   (allowlisted, stdio-only), not portable to other clients. See [Part 1](#part-1--mcp-findings-for-the-comms-mcp-server).

2. **Three high-value fixes in the current hub are invisible from the CLI surface** and should be
   considered alongside the naming/MCP epic: (a) the hub silently drops prompts for slow consumers
   while marking them *delivered*; (b) agent identity is per-run, not stable — names and the
   registry die with the hub process; (c) there is no persistence, so a restart loses every message.
   See [Part 2](#part-2--candidate-features-beyond-the-current-plan).

---

## Part 1 — MCP findings for the comms MCP server

### 1.1 Spec state 2026

- Two revisions matter: **2025-11-25** ("Legacy" handshake era — what current clients speak) and
  **2026-07-28** ("Modern", released ~2 weeks ago; no client advertises it yet). Both live in the
  spec repo `schema/`; `2026-07-28` is the newest tag.
  https://github.com/modelcontextprotocol/specification (schema dir + tags)
- Modern revision removes the `initialize` handshake (stateless, capabilities in `_meta`) and adds
  `server/discover`; adds `subscriptions/listen` for server→client change streams.
  https://modelcontextprotocol.io/specification/2026-07-28/changelog.md
- **Conclusion: speak the 2025-11-25 handshake protocol** (`initialize` → `notifications/initialized`,
  declare `tools`, optionally `tools.listChanged`).

### 1.2 THE verdict: no server-initiated work delivery

- Server→client messages in core MCP are **status/metadata only**: `notifications/message` (log),
  `notifications/progress`, `list_changed` × 3, `notifications/resources/updated`,
  `notifications/tasks/status`, `notifications/elicitation/complete`. **None carries a work item.**
  https://github.com/modelcontextprotocol/specification/blob/2025-11-25/schema/2025-11-25/schema.ts
- **SEP-2260 (Final):** server requests (`roots/list`, `sampling/createMessage`,
  `elicitation/create`) MUST be nested inside a client request; *standalone server-initiated
  requests outside notifications are prohibited*. A background push loop is shown as the disallowed
  example.
  https://modelcontextprotocol.io/seps/2260-Require-Server-requests-to-be-associated-with-Client-requests.md
- Elicitation/authorization are nested-in-request by design; the 2026-07-28 MRTR pattern keeps
  that (client retries the *same* request). The **Tasks** extension is client-polled handles, not a
  delivery channel. The **Triggers & Events Working Group** (charter 2026-03-24) exists precisely to
  add standardized push, but is still at the "Ideating" stage.
  https://modelcontextprotocol.io/extensions/tasks/overview.md ·
  https://modelcontextprotocol.io/community/working-groups/triggers-events.md
- **Only working exception:** Claude Code **Channels** — `notifications/claude/channel` under the
  `claude/channel` experimental capability; research preview, Anthropic-allowlisted, stdio-only,
  Claude-specific. Not portable. https://code.claude.com/docs/en/channels-reference

### 1.3 Design implication

The comms MCP server must be **client-polled**:

- Expose `list_agents`, `send_message`, `await_message` (+ `status`). The model calls
  `await_message` between turns; it **blocks until a peer prompt arrives** (or a short timeout,
  then returns empty so the agent re-calls).
- Long-poll safety (client-side limits): emit `notifications/progress` while waiting, keep polls
  ≤ ~5 min, prefer stdio (Claude Code gives 30-min idle / ~28h wall clock on stdio; Gemini CLI
  defaults a 10-min per-request MCP timeout).
  https://code.claude.com/docs/en/mcp · https://google-gemini.github.io/gemini-cli/docs/tools/mcp-server.html
- Tool details: names in `[A-Za-z0-9_.-]`; `inputSchema` = JSON Schema 2020-12; business failures
  return `isError: true` with actionable text (not a JSON-RPC error).
  https://modelcontextprotocol.io/specification/2025-11-25/server/tools

### 1.4 Go SDKs vs stdlib-only

- Official `modelcontextprotocol/go-sdk` is Tier 1, but pulls non-stdlib deps (golang-jwt,
  jsonschema-go, segmentio/encoding, oauth2). `mark3labs/mcp-go` targets 2025-11-25 but also has
  third-party deps. https://github.com/modelcontextprotocol/go-sdk · https://github.com/mark3labs/mcp-go
- **Hand-rolling a stdlib server is reasonable:** the needed surface is small (JSON-RPC 2.0 framing
  over stdio, initialize handshake, `tools/list`/`tools/call`, optional notifications) against a
  ~2.5k-line authoritative schema. Main effort: JSON-Schema input validation + JSON-RPC error codes
  (`-32602`, `-32601`).

### 1.5 pi constraints

- **pi has no built-in MCP client** ("intentionally does not include built-in MCP"; push
  workflow-specific behaviour into extensions). So for pi, the comms integration **is the
  extension** (`coms-net.ts`) talking HTTP+SSE directly — an MCP server role doesn't apply to pi.
  https://pi.dev/docs/latest/usage
- **GUI changes go through the extension API only** — `ctx.ui.*` helpers, `ctx.ui.custom()`,
  `setWidget`, register*Renderer — documented and confirmed. https://pi.dev/docs/latest/extensions

---

## Part 2 — candidate features beyond the current plan

Ranked by value-for-effort for a small, single-maintainer, collaborative project. Baseline:
stdlib-only Go; in-memory registry + queue; bearer-token auth; loopback default; one harness (pi).

### 2.1 Fix delivery guarantees + reconnect catch-up — HIGH, LOW-MOD cost
**The single highest-value correctness fix.** SSE per-agent streams are buffered at 256 frames and
`sendToStreamLocked` drops on a full buffer (`select default`) **while the message is simultaneously
marked `delivered`** (internal/server/server.go:1130, 818). A slow consumer silently loses prompts
the hub believes it delivered. On reconnect, only `hello` + `pool_snapshot` are sent — the missed
queue is never redelivered, and clients never send `Last-Event-ID` even though the stream emits
`id:`. Fix: park + redeliver (or block) instead of drop, and track delivery state separately from
queue state. Stdlib-only; reuses existing `id:` framing.

### 2.2 Stable agent identity & name reservation — HIGH, MODERATE cost
Identity is per-run today: reconnect = new `session_id`, name survives only if re-passed before
someone else claims it, and the whole registry dies with the hub. A small persistent store (the
`state` package already writes under `~/.pi/coms-net`) lets names survive reconnect/restart and
matches how pi already thinks (`/resume`, `--name`). This **unblocks** 2.3, 2.4 and the name-squatting
policy (treat the name as the address; disambiguate on collision rather than renaming — as Claude
Code cross-session does). Sources: Claude Code cross-session
(https://code.claude.com/docs/en/cross-session-messaging), OpenAI Agents SDK `name` as required
routing property (https://openai.github.io/openai-agents-python/agents/), LangGraph `thread_id`.

### 2.3 Message persistence + conversation replay — MEDIUM-HIGH, LOW-MOD cost
Messages live in a `map`; the TTL sweep deletes them 30 min after completion; a hub restart loses
everything. But `conversation_id` is already on the wire. The cheap version: append-only JSONL
message log (stdlib) + `GET /v1/conversations/:id`. Doubles as the audit trail (2.8).

### 2.4 Per-agent credentials + HMAC message signing — MEDIUM now / HIGH later, LOW cost
The shared bearer token authenticates the *channel*, not the *sender* — any token holder can
register under an arbitrary `session_id` or send as any session (only the response path is bound).
Per-agent keys derived from the shared secret + HMAC over the message (`crypto/hmac`, stdlib) lets
the hub and receivers prove who said what, and adds idempotency (reject a re-submitted `msg_id`).
Important **before** the hub ever leaves loopback or becomes an MCP server. A2A already signs agent
cards with JWS (https://a2a-protocol.org/latest/specification/).

### 2.5 `/v1/metrics` — MEDIUM, very low cost
`context_used_pct`, `queue_depth`, `status` are already reported on heartbeats; mean message
latency is derivable from `DeliveredAt → CompletedAt`. A `/v1/metrics` endpoint in Prometheus text
format is close to free and covers most ops needs for one maintainer.

### 2.6 Poisoning/spam controls — MEDIUM, LOW cost
`MaxInbox` (100) and `MaxHops` (5) already exist. Missing: per-sender rate limiting,
identical-message suppression, and a per-prompt length cap. Claude Code's cross-session channel is a
concrete template (rate-limit repeats, cap 50 accepted / 100 held inbound).
https://code.claude.com/docs/en/cross-session-messaging

### 2.7 Human-in-the-loop approval — MEDIUM, LOW-MOD cost
A `requires_approval` flag on a message (status stays `queued` until approved) + an approve/reject
endpoint. Design the authority model **before** trusting agent-origin approvals: per Claude Code,
"a message from another session never counts as your consent" and peers cannot approve permission
prompts on your behalf. https://code.claude.com/docs/en/agent-teams

### 2.8 Purpose/capability-based discovery — MEDIUM, LOW cost
`GET /v1/agents` has no `?purpose=` filter even though `purpose` is on every card; no broadcast
primitive either. The snapshot mechanism already exists; adding intent-based routing is nearly free.
Compare: Claude subagent `description`-driven delegation
(https://code.claude.com/docs/en/sub-agents), OpenAI handoff routing.

### 2.9 Optional TLS — LOW-MED, LOW cost
`crypto/tls` is stdlib; `--tls-cert/--tls-key` (or a documented reverse-proxy mode) is cheap, but
cert management is a burden. Value rises mainly when the MCP server lands, since MCP clients expect
streamable HTTP + OAuth over HTTPS.

### 2.10 Webhooks / outbound mirror — LOW, defer
No outbound webhooks today, but comms already has a native push primitive (the SSE stream), so
webhooks are an adapter, not a new capability. Interesting later for a "mirror hub to web UI" mode.

### 2.11 Clock skew — no action now
Single-process hub: all timestamps come from one server clock and `expires_at` is hub-authored, so
skew is a non-issue. Add a one-line wire note ("timestamps are hub-authoritative"). It only becomes
a problem if the hub fans out to replicas.

---

## Part 3 — Synthesis

What this means for the existing plan (naming + MCP epic):

1. **MCP epic, ticket "inbound prompt delivery":** change from "MCP notifications" to a **blocking
   `await_message` long-poll tool** (client-polled), stdio transport, 2025-11-25 protocol, hand-rolled
   stdlib server. Optionally add a Claude Code Channels capability later.
2. **Naming (`comms join -n/--name`):** keep as-is, but pair it with **stable identity** (2.2) —
   otherwise the chosen name is still lost on the next `/resume`.
3. **Do 2.1 (delivery guarantees) as part of the first hub-touching epic** — it is a live data-loss
   bug, independent of MCP.
4. **2.2 and 2.4 (identity + signing)** are prerequisites for a comms MCP server that talks to the
   outside world; sequence them before exposing the hub via MCP.

---

## Sources (Thread B; Thread A citations are inline)

- MCP spec 2025-11-25: https://modelcontextprotocol.io/specification/2025-11-25
- MCP spec 2026-07-28 changelog: https://modelcontextprotocol.io/specification/2026-07-28/changelog.md
- Spec repo & schema: https://github.com/modelcontextprotocol/specification
- SEP-2260 (server requests nested in client requests): https://modelcontextprotocol.io/seps/2260-Require-Server-requests-to-be-associated-with-Client-requests.md
- SEP-986 (tool names): https://modelcontextprotocol.io/seps/986-specify-format-for-tool-names.md
- Tasks extension: https://modelcontextprotocol.io/extensions/tasks/overview.md
- Triggers & Events WG: https://modelcontextprotocol.io/community/working-groups/triggers-events.md
- Official Go SDK: https://github.com/modelcontextprotocol/go-sdk
- mark3labs/mcp-go: https://github.com/mark3labs/mcp-go
- Claude Code MCP + Channels: https://code.claude.com/docs/en/mcp · https://code.claude.com/docs/en/channels-reference
- Gemini CLI MCP: https://google-gemini.github.io/gemini-cli/docs/tools/mcp-server.html
- Cursor MCP: https://cursor.com/docs/mcp
- pi docs: https://pi.dev/docs/latest/extensions · https://pi.dev/docs/latest/usage
