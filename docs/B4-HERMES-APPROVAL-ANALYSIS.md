# B.4: Hermes Browser Action Approval Analysis

## Hermes Source Analysis

**Pinned Version**: `05eecbcd972c8737ebc7722ea08aab47fb538043`  
**Repository**: https://github.com/NousResearch/hermes-agent

### Approval Architecture

Hermes has a comprehensive approval system:

#### Core Components

1. **Approval Callback Mechanism** (`agent/codex_runtime.py`, `agent/transports/codex_app_server_session.py`):
   - `approval_callback` parameter passed through session initialization
   - Two decision points: `_decide_exec_approval` (terminal commands) and `_decide_apply_patch_approval` (file changes)
   - Returns `"accept"`, `"reject"`, or `"permanently_accept"` based on user choice

2. **Approval Context** (`tools/approval_context.py`):
   - Provides `_get_approval_callback()` to retrieve current approval handler
   - Contextvars-based propagation across async boundaries

3. **YOLO Mode** (`tools/approval.py`, `_YOLO_MODE_FROZEN`):
   - Bypass mechanism via `approvals.mode: off`, `/yolo` command, or `HERMES_YOLO_MODE` env var
   - `is_approval_bypass_active()` checks current state
   - **XBot note**: Our config sets `approvals.mode` - need to verify this doesn't bypass browser approvals

4. **Runtime Self-Protection** (`agent/runtime_self_protection.py`):
   - Floor-level blocking via `tools.approval._floor_block`
   - **Cannot be bypassed** by yolo/off modes - enforced at system level

#### Browser Tool Implementation

From `agent/browser_provider.py`:
- **Cloud browser providers**: Pluggable backend system (Browserbase, Browser Use, Firecrawl)
- Provider selected via `browser.cloud_provider` config
- Lifecycle: `create_session()` → CDP WebSocket → `close_session()`
- Session metadata includes `cdp_url`, provider session ID, expiry

**Current XBot Config** (from `/workspace/internal/engine/hermes/provision.go:284`):
```go
cfg["browser"] = map[string]any{
    "backend": "browser-use", 
    "cdp_url": "http://127.0.0.1:9222", 
    "allow_private_urls": false  // ✅ Fixed in B.5
}
```

#### Browser Action Approval Status

**Searched patterns**:
- `browser_exec` mentions: Found in tool files but no explicit consequential-action approval gating
- Approval hooks: Only for `terminal` (exec) and `file` (apply_patch) operations

**Finding**: **Hermes does NOT have built-in browser action approval hooks** at the version we're using. Browser tools bypass the approval system that exists for terminal and file operations.

### Implementation Options for B.4

#### Option 1: Hermes Plugin/Hook (Preferred if feasible)
- Add approval callback for `browser_*` tools
- Hook into Hermes' existing approval infrastructure
- **Challenge**: Requires Hermes source modification or plugin system
- **Benefit**: Integrated with Hermes' approval UI and logic

#### Option 2: XBot-Side Interception (Feasible without Docker)
- Intercept `browser_exec` tool calls in XBot's Hermes driver
- Parse action intent from tool parameters
- Send approval request via XBot inbox before forwarding to Hermes
- **Challenge**: Need to reliably detect "consequential" actions
- **Benefit**: No Hermes modification, works with existing system

#### Option 3: Configuration-Based Restriction
- Set `browser.backend: disabled` or equivalent
- Only enable for bots where user explicitly approves browser access
- **Challenge**: All-or-nothing, not action-granular
- **Benefit**: Simple, immediate

### Consequential Browser Actions (to gate)

Based on `docs/B4-BROWSER-APPROVAL-PLAN.md` and common sense:
1. **Form submissions** - `<form>` POST/submit actions
2. **Purchases/transactions** - checkout, payment, order placement
3. **Send/post/publish** - social media posts, emails, messages
4. **Delete operations** - account deletion, data removal
5. **Logged-in site actions** - actions on sites with active cookies/auth

Detection heuristics:
- URL patterns: `*/checkout`, `*/cart`, `*/post`, `*/delete`, `*/buy`
- Action verbs in tool call: "submit", "post", "buy", "delete", "send", "purchase"
- HTTP methods: POST, PUT, DELETE (vs. GET/read-only)
- Cookie presence (indicates logged-in session)

### Real-Engine Conversation Flow Analysis

From `/workspace/internal/engine/hermes/driver.go`:

#### Lifecycle
1. **Session Start** (`Start()` method):
   - Opens WebSocket to `hermes_host.py` RPC server
   - Sends `connect` message with session ID
   - Awaits `connected` response
   
2. **Turn Execution** (`Send()` method):
   - Sends `message` RPC with user text
   - Reads SSE stream of events
   - Parses events into `engine.Event` structs

3. **Event Streaming** (`readEvent()` private):
   - SSE format: `event: <type>\ndata: <JSON>\n\n`
   - Event types: `text`, `thinking`, `tool.start`, `tool.complete`, `complete`, `error`, `stop`, `usage`
   - Tool results in `tool.complete` → `result` field (JSON string)

#### Potential Failure Points (Hypotheses for Real-Engine Bugs)

1. **WebSocket/RPC Connection**:
   - **Hypothesis 1**: `hermes_host.py` not starting in container (Python env issue, missing deps)
   - **Hypothesis 2**: Port 9731 (RPC) blocked or already in use
   - **Symptom**: `driver.go:Start()` hangs on `Connect()` or `read()` timeout
   - **Diagnostic needed**: Log WebSocket dial attempts, first RPC response

2. **SSE Parsing**:
   - **Hypothesis 3**: Hermes sends malformed SSE (missing `\n\n`, wrong `data:` format)
   - **Hypothesis 4**: Event JSON doesn't match expected schema (Hermes version mismatch)
   - **Symptom**: `readEvent()` returns errors, turn never completes
   - **Diagnostic needed**: Log raw SSE lines before JSON parse, dump malformed events

3. **Session State**:
   - **Hypothesis 5**: Session ID collision or stale session not cleaned up
   - **Hypothesis 6**: Container filesystem corruption (config.yaml, memory DB)
   - **Symptom**: Hermes returns `error` event with "session not found" or similar
   - **Diagnostic needed**: Log session lifecycle (create, connect, cleanup), verify config.yaml writes

4. **Model API**:
   - **Hypothesis 7**: Model Proxy unreachable from container (networking, DNS)
   - **Hypothesis 8**: Worker key invalid or expired
   - **Symptom**: Hermes returns API error, turn fails immediately
   - **Diagnostic needed**: Log Model Proxy requests/responses from Hermes side

5. **Streaming/Timeout**:
   - **Hypothesis 9**: SSE stream stalls mid-turn (no `complete` event)
   - **Hypothesis 10**: Turn timeout too aggressive, kills working session
   - **Symptom**: Frontend shows spinning, backend times out
   - **Diagnostic needed**: Log time between events, explicit timeout triggers

6. **Tool Execution**:
   - **Hypothesis 11**: Tool (terminal, browser, file) fails but error not propagated
   - **Hypothesis 12**: Approval timeout when inbox unreachable
   - **Symptom**: Bot stuck in "working" state, no response
   - **Diagnostic needed**: Log every `tool.start`/`tool.complete` pair, detect orphaned tools

## Next Steps (When Docker Available)

1. **Implement Option 2** (XBot-side interception):
   - Add `browser_exec` detection in `driver.go`
   - Parse action intent and trigger approval for consequential actions
   - Unit tests with mocked Hermes responses

2. **Add Diagnostic Logging** (do now without Docker):
   - WebSocket connection attempts
   - RPC message send/receive
   - SSE stream parsing
   - Session lifecycle events
   - Tool start/complete tracking

3. **Test in Real Environment**:
   - Reproduce owner's conversation failure with logs
   - Verify approval flow for browser actions
   - Confirm no regression in Hermes communication

## Status

- ✅ Analyzed Hermes approval architecture
- ✅ Identified lack of browser action approval in current Hermes
- ✅ Documented implementation options
- ✅ Listed conversation failure hypotheses
- ⏳ **Diagnostic logging implementation** (next task)
- ⏳ Browser approval interception (requires Docker for testing)
