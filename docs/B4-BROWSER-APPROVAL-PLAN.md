# B.4 Implementation Plan: Browser Action Approval

## Goal
Require human approval for consequential browser actions (form submits, purchases, posts, sends, deletes) on logged-in sites.

## Technical Approach

### Option 1: Extend Hermes Approval Patterns (Recommended)
Hermes already has `DANGEROUS_PATTERNS` in `tools/approval.py`. Add browser action patterns:

```python
# In Hermes container's tools/approval.py
BROWSER_DANGEROUS_PATTERNS = [
    r'submit.*form',
    r'click.*(submit|buy|purchase|order|pay|delete|remove|send)',
    r'type.*(password|credit.*card|cvv)',
    r'press.*(enter|return).*form',
]
```

### Option 2: xBot-Side Interception
Add approval hook in `internal/engine/hermes/driver.go`:

```go
// Intercept browser_exec tool calls
func (d *Driver) checkBrowserAction(toolName string, args map[string]any) (needsApproval bool, reason string) {
    if toolName != "browser_exec" {
        return false, ""
    }
    
    code, _ := args["code"].(string)
    dangerous := []string{
        "submit", "click.*buy", "click.*purchase",
        "click.*delete", "click.*send", "form",
    }
    
    for _, pattern := range dangerous {
        if matched, _ := regexp.MatchString(pattern, strings.ToLower(code)); matched {
            return true, "危險的瀏覽器操作需要核准：" + pattern
        }
    }
    return false, ""
}
```

### Option 3: CDP Proxy Layer
Wrap Chrome DevTools Protocol calls to detect form submissions:

```go
// internal/box/cdp_proxy.go
func (p *CDPProxy) InterceptFormSubmit(ctx context.Context, req *cdp.Request) error {
    if req.Method == "DOM.querySelector" && strings.Contains(req.Params, "form[action]") {
        // Create approval request
        return p.requestApproval(ctx, "表單提交", req)
    }
    return nil
}
```

## Implementation Status

**TODO**: Full implementation requires:
1. Modify Hermes container's approval.py (needs container rebuild)
2. Add browser action patterns to DANGEROUS_PATTERNS
3. Update Hermes config to enable browser approval mode
4. Add tests for browser action approval flow
5. Update SOUL.md to explain browser approval to the Bot

**Time Estimate**: 6-8 hours (requires Hermes source access and container rebuild)

## Verification

```bash
# Test: Bot tries to submit a form
# Expected: Approval card appears in inbox
curl -X POST http://localhost:8780/api/v1/chat.send \
  -d '{"bot_id":"bot1","text":"請到 example.com 填表單並提交"}'

# Check inbox for approval
curl http://localhost:8780/api/v1/inbox.list
# Should contain: "需要核准：提交表單到 example.com"
```

## Security Impact

**Before**: Bot can submit forms, make purchases without approval  
**After**: All consequential browser actions require explicit human approval

**Risk Mitigation**: 
- Whitelist safe actions (readonly operations, navigation)
- Timeout approvals after 10 minutes
- Log all browser actions to audit trail
