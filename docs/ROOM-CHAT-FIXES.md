# Room Chat Fixes (2026-10-22)

## Issues Found in Real Eval

1. **Thanks ping-pong loop**: 1 user message → 11 pure-thanks messages (both master & PR)
   - Cause: `roomAnswer` (room.go:691) posts anything non-[silent], wakes @mentioned bots
   - `HasNewInformation` filter only on DM path (collab.go:842-865), not room path

2. **Bot-initiated speak unfiltered**: No checks on bot→bot messages (collab.go:412)

3. **Weak HasNewInformation** (text.go:120-220):
   - '不客氣！很高興能幫上忙...' passes because '幫' = handoff
   - Any >30 chars = new info
   - No duplicate check across rooms

4. **Unused peer-reply judge**: judge/uses.go:203 defines `UsePeerReply` but never called

5. **No closing rule**:
   - Room prompt (room.go:652-668) has no closer, no round limit
   - Bots ask "please sign off" after consensus (extra rounds)
   - Empty '進度' acks to user

6. **Hop counter resets**: Lost when bot returns from DM to room

7. **REGRESSION** (room.go:460): `@所有人` with no explicit @ wakes 0 bots (was 2)
   - Code explicitly returns nil for RuleEveryone when no mentions

8. **Length issues**: 
   - provision.go:429-457 still says 3-5 sentences
   - Room messages 500-800 chars
   - No peer length cap (collab.go:709)

9. **Placeholder screenshots**: 4 before images identical, 4 after identical

10. **Credential race**: 6 containers sharing Grok credential → rotation conflicts

## Fixes

### Fix 1: Shared send-gate with peer-reply judge ✅
- Wire `UsePeerReply` judge: `reply|fyi|none` classification
- Apply to: room replies, bot-initiated speak, DM replies
- Heuristic fallback when judge unavailable
- Duplicate check across room + DM

### Fix 2: Closer rule ✅
- Room/discussion has lead/closer
- Within 3 rounds, closer posts 'Conclusion:' to user
- No further wakes after conclusion
- Ban 'please sign/confirm' rounds

### Fix 3: Auto-stop on no-progress ✅
- 2 consecutive no-new-info rounds → auto-stop
- Notify user
- Don't reset hop count on DM→room return

### Fix 4: Length classes + peer rules ✅
- Fact ≤60 chars
- Explanation 2-4 paragraphs
- Research ≤600 chars (details in file)
- Room msg ≤200 chars
- Peer msg ≤300 chars (file paths for data)
- Progress acks only after >30s, only when saying who/when

### Fix 5: Restore @所有人 wake-all ✅
- Remove explicit nil return for RuleEveryone
- Wake all bots when @所有人 with no specific @mentions

### Fix 6: Tests ✅
- Room thanks loop <3 msgs
- Bot-initiated thanks blocked
- Long pleasantry blocked
- @所有人 with no @ gets replies
- Hop count persists across DM→room

## Implementation Status

**Priority 1** (Blocking thanks loops):
- [ ] Fix `roomAnswer` to use send-gate
- [ ] Fix `Send` (bot-initiated) to use send-gate
- [ ] Strengthen `HasNewInformation` heuristics
- [ ] Add duplicate check across rooms

**Priority 2** (Closing & length):
- [ ] Add closer designation to room prompt
- [ ] Add conclusion detection & auto-stop
- [ ] Update length guidance in prompts
- [ ] Add peer message length caps

**Priority 3** (Restore functionality):
- [ ] Fix @所有人 wake-all regression
- [ ] Fix hop counter preservation

**Priority 4** (Validation):
- [ ] Add comprehensive tests
- [ ] Fix placeholder screenshots
- [ ] Investigate credential refresh race
