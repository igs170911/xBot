# Bot Conversation Loop - Root Cause & Fix

## Root Cause

Bots engage in endless meaningless back-and-forth due to insufficient loop prevention:

### Existing Mechanisms (Insufficient)
1. **Hop Limit**: Set to 20 (too high, was 6 in LazyBoy)
2. **[silent] Mechanism**: Exists but bots don't reliably use it
3. **Guidance Only**: Rules say "don't send thanks/acknowledgements" but no enforcement
4. **No Deduplication**: Identical/similar messages not detected

### Why Loops Occur
1. Bot A sends task to Bot B
2. Bot B replies with acknowledgement ("got it", "thanks", "received")
3. Bot A sees reply, thinks it needs to respond, sends another acknowledgement
4. Loop continues for up to 20 hops before stopping
5. No detection of meaningless content
6. No enforcement that replies must add new information

## Fix Implemented

### 1. Acknowledgement Detection (`internal/collab/text.go`)
Added `IsAcknowledgement()` function to detect meaningless replies:
- Pure acknowledgements: "收到", "謝謝", "好的", "了解", "明白", "OK", "thanks", "got it"
- Restatements without new info
- Too-short messages (< 20 chars) that aren't [silent]

### 2. Hop Limit Reduction (`internal/limits/tunable.go`)
Changed MaxHops from 20 → 6 (original LazyBoy value):
- Stops runaway conversations faster
- Forces escalation to human after reasonable back-and-forth

### 3. Reply Enforcement (`internal/collab/collab.go`)
Modified `reply()` function to:
- Treat acknowledgements same as [silent] - don't send them
- Only send replies that add value
- Log when acknowledgements are blocked

### 4. Persona Strengthening (`internal/engine/hermes/provision.go`)
Enhanced system rules:
- "Bot-to-bot replies MUST add new information or results"
- "Never reply with just acknowledgements, thanks, or 'got it'"
- "If nothing new to add, reply [silent]"
- "Restate only when genuinely clarifying"

### 5. Room Behavior (`internal/collab/room.go`)
Strengthened rules:
- Only addressed bot replies (via @mention or assignment)
- Others stay silent unless they have unique new information
- No "me too" or agreement-only posts

## Testing

Added test cases (`internal/collab/loop_test.go`):
1. Two bots that would previously loop → now stops after acknowledgement
2. Identical message detection → second one blocked
3. Hop limit enforcement → stops at 6
4. [silent] compliance → no reply sent

## Verification Commands

```bash
# Run loop prevention tests
go test ./internal/collab/ -v -run TestLoopPrevention

# Check that existing collab tests still pass
go test ./internal/collab/...

# Verify hop limit change
grep -A2 "MaxHops.*int" internal/limits/tunable.go
```

## Monitoring

When deployed, watch for:
- Reduced bot-to-bot message volume
- Fewer conversations exceeding 3-4 hops
- More [silent] outcomes
- User reports of "bot won't reply" (may need tuning if too aggressive)
