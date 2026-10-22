# Busy Work Analysis: Root Causes and Solutions

## Executive Summary

XBot currently feels "busy without planning or reaching conclusions" due to several systemic issues in prompts, runtime structure, and length calibration. This document audits all system prompts and identifies root causes with file paths for fixes.

---

## 1. System Prompt Audit

### 1.1 Hermes SOUL.md (`internal/engine/hermes/provision.go`)

**Location**: Lines 474-537, `func Soul(p Profile) string`

**Current State**:
- **Length**: ~80+ lines of dense instructions
- **Structure**: No enforced plan-first approach
- **Issues**:
  - Line 499: "一個要求背後的整件事一次做完整" encourages doing everything without stating plan first
  - Line 502-506: "交代工作就直接做到完成：不要停下來問...說要做的同一輪就做完" - pushes to act immediately without planning
  - Line 510: "互不相關的搜尋...一起送出" - good for efficiency but no planning step mentioned
  - Line 456-457: Only shows EXAMPLE outputs, not example planning structure
  - **No conclusion requirement**: Nowhere does it say "always end with clear conclusion + next step"
  - **No milestone reporting rule**: Lists all process steps instead of key decisions/blockers

**Problems**:
1. **Missing plan step**: Instructions jump straight to execution
2. **No stop/conclusion requirement**: Bot can end mid-work
3. **Process-heavy**: Told to avoid narrating process but not given alternative structure

### 1.2 Runtime OwnerNote (`internal/runtime/runtime.go`)

**Location**: Lines 298-304, `const OwnerNote`

**Current State**:
```go
"是交代工作就在這一輪做到完成：說要做的事立刻呼叫工具去做，不要問要不要開始，
也不要寫「接下來去做某某」就停下來。"
```

**Problems**:
1. **Anti-pattern**: Explicitly禁止 "接下來去做某某" which COULD be a valid plan statement
2. **No planning instruction**: Tells bot to act immediately, not plan first
3. **No conclusion enforcement**: Doesn't require wrapping up with decision/result

### 1.3 ACP Instructions (`internal/runtime/computers.go`)

**Location**: Lines 340-353, `func (c *Computers) Instructions()`

**Current State**:
- Uses same `hermes.Soul()` function (line 351-352)
- Adds memory note (line 356-370) and skill index (line 396-422)
- **Same issues as SOUL.md**: No plan-first, no conclusion requirement

### 1.4 Reply Style Rules (`internal/engine/hermes/provision.go`)

**Location**: Lines 426-457, `func replyStyleText(s ReplyStyle)`

**Current State**:
- Line 430: "簡單的問題三到五句、兩段以內" - HARD CAPS causing truncation
- Line 432-434: Different caps for Normal/Detailed but still rigid
- Line 440-447: Good conversational principles BUT mixed with length constraints
- Line 460-466: `replyReminder()` - short reminder at end, doesn't reinforce planning

**Problems**:
1. **Hard character/paragraph caps**: "三到五句、兩段以內" leads to premature cutoff
2. **No reply-class system**: Doesn't distinguish chitchat vs task report vs deep analysis
3. **Length before content**: Prioritizes brevity over completeness

### 1.5 Judge System Prompt (`internal/judge/model.go`)

**Location**: Lines 16-21, `const systemPrompt`

**Current State**:
```go
"You are XBot's fast judge inside an application. You only classify or write a very short text; you never act."
"Reply with one JSON object only, no markdown: {\"label\": string, \"p\": number, \"why\": string}."
```

**Assessment**: ✅ **Good** - Judge prompt is tight, focused, output-constrained. No busy work issues here.

### 1.6 Runtime Loop (`internal/runtime/runtime.go`)

**Location**: Lines 495-620 (`func (r *Runtime) run()`) and 620-790 (`consume`, `handle`)

**Current Execution Flow**:
1. Load prompt/context → Start engine session (lines 500-578)
2. Event loop: consume all events until `Done` or `Error` (lines 620-790)
3. Watchdog monitors idle time, interrupts if stuck (lines 1195-1260)
4. Finish: save message, set verdict, emit done event (lines 791-820)

**Problems**:
1. **No plan detection**: Never checks if bot stated a plan first
2. **No conclusion detection**: Never verifies final message is a proper conclusion
3. **No redundant tool call detection**: Same tool+args can be called repeatedly (lines 650-665 track open tools but not redundancy)
4. **No no-progress turn detection**: Watchdog only watches idle time, not whether work is progressing
5. **Progress spam**: Line 702-710 emits milestones but no cap on frequency

---

## 2. Root Causes by Symptom

### 2.1 "Looks Busy Without Planning"

**Symptoms**:
- Bot jumps into action immediately
- Users don't know what bot will do until it's doing it
- Multi-step work feels chaotic

**Root Causes**:
1. **SOUL.md line 499-506**: Explicitly tells bot to do entire thing at once without planning step
2. **OwnerNote line 303**: Prohibits saying "接下來去做某某" (which could be a plan)
3. **No planning requirement**: Nowhere in prompts says "For multi-step work, first state a one-line plan"
4. **Runtime doesn't enforce**: Lines 495-578 just start engine, never check for plan

**Files to Fix**:
- `internal/engine/hermes/provision.go` (Soul function)
- `internal/runtime/runtime.go` (OwnerNote constant, runtime.run)

### 2.2 "Doesn't Reach Conclusions"

**Symptoms**:
- Bot stops mid-work without summary
- Unclear what was accomplished
- Users have to ask "so what happened?"

**Root Causes**:
1. **No conclusion requirement**: SOUL.md never says "always end with clear conclusion + next step"
2. **Runtime accepts any Done**: Lines 775-790 finish on any `EventDone` without checking for conclusion
3. **Reply style focuses on length, not structure**: Lines 440-450 talk about conversation style but not conclusion structure

**Files to Fix**:
- `internal/engine/hermes/provision.go` (add conclusion rule)
- `internal/runtime/runtime.go` (detect missing conclusion, request one)
- `internal/runtime/taskdone.go` (conclusion verification logic)

### 2.3 "Progress Spam"

**Symptoms**:
- Too many milestone notifications
- Chat feels busy but not productive
- Every tool call feels like "progress"

**Root Causes**:
1. **Too many milestone tools**: Lines 702-710 emit milestone for specific tools, but list might be too broad
2. **No meaningful-only filter**: Doesn't check if milestone represents actual decision/blocker/result
3. **Milestone definition**: `internal/runtime/milestones.go` might mark too many tools as milestones

**Files to Fix**:
- `internal/runtime/milestones.go` (narrow milestone definition)
- `internal/engine/hermes/provision.go` (instruct: report only meaningful milestones)

### 2.4 "Reply Length Miscalibrated"

**Symptoms**:
- Earlier too long (verbose reports)
- Now too short (truncated answers)
- Doesn't adapt to ask complexity

**Root Causes**:
1. **Hard caps**: Line 430 "三到五句、兩段以內" is rigid
2. **No reply-class system**: Doesn't distinguish:
   - Chitchat (few words)
   - Quick answer (1-2 para)
   - Task report (结果+blocker, ~3 para)
   - Deep report (comprehensive, ~6+ para)
3. **No post-check**: Never validates reply length vs expected class

**Files to Fix**:
- `internal/engine/hermes/provision.go` (replace hard caps with reply-class system)
- `internal/runtime/runtime.go` (add post-check rewrite for outliers)
- New: `internal/runtime/replyclass.go` (classification + target ranges)

### 2.5 "Bot<->Bot Work Unclear"

**Symptoms**:
- Delegating bot doesn't state goal clearly
- Receiving bot doesn't return clear result/blocker
- Intermediate bots spam progress updates

**Root Causes**:
1. **Collab prompt unclear**: `internal/collab/room.go` lines 532-600 `roomPrompt` doesn't enforce goal+done-criteria structure
2. **No result requirement**: `internal/collab/collab.go` lines 733-850 `finished` accepts any verdict/text without checking for clear result/blocker statement
3. **Same prompts for owner & bot**: SOUL.md applies to all conversations, doesn't differentiate bot<->bot collaboration

**Files to Fix**:
- `internal/collab/room.go` (enforce goal+done-criteria in delegation)
- `internal/collab/collab.go` (validate result/blocker in finished)
- `internal/engine/hermes/provision.go` (add bot<->bot section to SOUL.md)

---

## 3. Redundant Tool Calls & No-Progress Detection

**Current State**: None implemented

**Missing**:
1. **Redundant tool call detection**:
   - Lines 650-665 track `openTools` (in-flight) but not history
   - No check for: same tool name + same arguments called twice
   - Can loop indefinitely on failing tool

2. **No-progress turn detection**:
   - Watchdog (lines 1195-1260) only monitors idle time
   - Doesn't detect: many tool calls but no actual progress toward goal
   - Doesn't detect: repeated thinking without action

**Files Needing**:
- New: `internal/runtime/progress.go` (track tool call history, detect loops)
- `internal/runtime/runtime.go` (integrate progress tracking, force conclusion on no-progress)

---

## 4. Detailed File Paths for Fixes

| Issue | File | Line(s) | Fix Type |
|-------|------|---------|----------|
| No plan-first rule | `internal/engine/hermes/provision.go` | 490-510 | Add to SOUL.md base rules |
| Anti-plan OwnerNote | `internal/runtime/runtime.go` | 298-304 | Rewrite OwnerNote |
| No conclusion requirement | `internal/engine/hermes/provision.go` | 460-466 | Add to replyReminder |
| Hard length caps | `internal/engine/hermes/provision.go` | 430-434 | Replace with reply-class |
| No conclusion detection | `internal/runtime/runtime.go` | 775-790 | Add detection in finish() |
| No plan enforcement | `internal/runtime/runtime.go` | 495-578 | Check plan in run() |
| No redundant detection | `internal/runtime/runtime.go` | 650-665 | Add history tracking |
| No progress detection | `internal/runtime/runtime.go` | 1195-1260 | Add to watchdog |
| Progress spam | `internal/runtime/milestones.go` | entire file | Narrow milestone list |
| Bot<->bot unclear | `internal/collab/room.go` | 596-650 | Add goal+done framing |
| No result validation | `internal/collab/collab.go` | 810-850 | Check result/blocker |

---

## 5. Proposed Solutions (High-Level)

### 5.1 Prompt Rules (Section 2 of task)

**Changes to `internal/engine/hermes/provision.go`**:

1. **Add plan-first rule** (line ~491):
   ```
   For multi-step work: state a one-line plan first ("I'll do A → B → C"),
   then execute, reporting only meaningful milestones (result, decision, blocker, changed plan).
   ```

2. **Replace hard length caps** (line 430-434) with reply-class:
   ```
   Size your reply to the ask:
   - Chitchat or simple fact: A few words to one short paragraph
   - Quick answer: 2-3 paragraphs (result + reason)
   - Task report: 3-4 paragraphs (what's done, what's left, blocker/decision)
   - Deep analysis: Full explanation as needed, each para one idea
   ```

3. **Add conclusion requirement** (line 465):
   ```
   Always end multi-step work with a clear conclusion: what was accomplished,
   what's left, and what the user/teammate should do next (or "nothing needed").
   ```

4. **Rewrite brevity rule**:
   ```
   Brevity = fewer ideas explained fully, not sentence fragments.
   Each idea gets complete sentences that connect naturally.
   ```

5. **Decide and proceed**:
   ```
   Decide and proceed instead of asking, unless consequential (money, external send, delete).
   For unclear scope: assume reasonably, state assumption, proceed.
   ```

### 5.2 Runtime Structure (Section 3 of task)

**New enforcement in `internal/runtime/runtime.go`**:

1. **Plan detection** (add after line 578):
   - After first few text chunks, check if plan stated
   - If multi-step work detected (e.g. multiple tool calls planned) but no plan in first 200 chars, note it

2. **Conclusion detection** (add in finish(), line ~780):
   - When engine says Done, check if last text chunk is a conclusion
   - Heuristics: contains result/summary words, no trailing "接下來" or "然後"
   - If missing: ask engine once more "Summarize what you accomplished"

3. **Redundant tool call cap** (new file `internal/runtime/progress.go`):
   - Track last N tool calls: {name, args_hash, timestamp}
   - If same name+args called 3 times in 5 minutes → force conclusion
   - If same tool fails 2 times → note as blocker

4. **No-progress turn detection**:
   - Track "progress indicators": new info acquired, file created, decision made
   - If 5+ tool calls but no progress indicators → force conclusion with "stuck" note

### 5.3 Bot<->Bot Work (Section 3 continued)

**Changes to `internal/collab/room.go`**:

1. **Delegation framing** (line ~540):
   - Delegating bot must state: goal + done-criteria (what constitutes completion)
   - Add to prompt: "State clearly: goal, success criteria"

2. **Result requirement** (line ~600):
   - Receiving bot must return: result (if done) OR blocker (if stuck) OR progress summary (if multi-day)

**Changes to `internal/collab/collab.go`**:

1. **Validate finished** (line ~740):
   - Check if text contains result/blocker statement
   - If verdict=answer but text doesn't explain what answer is → request clarification

### 5.4 Length Calibration (Section 4 of task)

**Reply-class system** (new file `internal/runtime/replyclass.go`):

```go
type ReplyClass int
const (
    ClassChitchat ReplyClass = iota // ~20-50 chars
    ClassQuick                       // ~100-300 chars
    ClassTask                        // ~300-600 chars
    ClassDeep                        // ~600+ chars
)

func ClassifyInput(text string) ReplyClass { ... }
func InRange(reply string, class ReplyClass) bool { ... }
```

**Post-check rewriter** (new in `internal/runtime/runtime.go`):

```go
func (r *Runtime) checkReplyLength(text, input string) string {
    class := replyclass.ClassifyInput(input)
    if replyclass.InRange(text, class) {
        return text
    }
    // Out of range: request rewrite via cheap model
    return r.rewriteReply(text, class)
}
```

---

## 6. Evaluation Metrics

To measure improvements, eval harness (`scripts/eval`) should track:

1. **Time-to-plan**: Chars before first plan statement appears (lower is better)
2. **Turns-to-conclusion**: Number of turns before task completes with conclusion (lower better)
3. **No-progress turn ratio**: Turns with tool calls but no progress / total turns (lower better)
4. **Conclusion present**: % of completed tasks ending with conclusion statement (100% target)
5. **Reply length vs class**: Std deviation of (actual length - target range midpoint) (lower better)
6. **Redundant calls**: % of tasks with 3+ identical tool calls (0% target)

---

## 7. Summary of Root Causes

| Symptom | Root Cause | File | Fix |
|---------|------------|------|-----|
| No planning | "Do entire thing at once" instruction | `provision.go`:499 | Add plan-first rule |
| No conclusions | No requirement to conclude | `provision.go`:460 | Require conclusion |
| Progress spam | Too many milestone tools | `milestones.go` | Narrow list |
| Miscalibrated length | Hard caps + no class system | `provision.go`:430 | Reply-class + ranges |
| Bot<->bot unclear | No goal+done framing | `room.go`:540 | Add framing |
| Redundant calls | No loop detection | `runtime.go`:650 | Add history tracking |
| No-progress turns | Only idle-time watchdog | `runtime.go`:1195 | Add progress detection |

---

## 8. Implementation Status (2026-10-22)

### Completed (Sections 1-2, Partial 3):

**Section 1: Audit** ✅ 
- Comprehensive audit of all system prompts completed
- Root causes documented with file paths and line numbers
- Analysis covers Hermes SOUL, OwnerNote, ACP instructions, reply style, judge, runtime loop

**Section 2: Prompt Rules** ✅
- **`internal/runtime/runtime.go` (OwnerNote)**: 
  - Changed from "直接做到完成" (do immediately) to "多步驟的事先說一行計畫" (multi-step: state plan first)
  - Added milestone-only reporting rule: "過程中只在關鍵決定、遇到障礙或改變計畫時簡短報告"
  - Added conclusion requirement: "做完給一句結論（做了什麼、有沒有完成、使用者要不要做什麼）"

- **`internal/engine/hermes/provision.go` (replyStyleText)**:
  - Removed hard length caps ("簡單的問題三到五句、兩段以內")
  - Added reply-class guidance: "簡單問題或事實：幾句話到一段。較複雜的要求：2-3 段（結果+原因）。多步驟任務報告：3-4 段（完成了什麼、還剩什麼、障礙或決定）。深入分析：完整說明"
  - Changed brevity rule from "不要寫成電報" to "簡潔 = 點子少一點、每個點子說完整，不是寫成電報句"
  - Added multi-step work structure: "多步驟工作：一開始先說一行計畫（「我會 A → B → C」），然後做，過程中只在關鍵決定、遇到障礙或改變計畫時簡短報告一句，做完給結論"
  - Bot-to-bot instruction: "給隊友的訊息（最後回答會送回給隊友時）：簡短精確，說清楚結果或卡在哪"

- **`internal/engine/hermes/provision.go` (replyReminder)**:
  - Updated from "不寫過程" to "多步驟工作說計畫→報里程碑→給結論"

- **`internal/engine/hermes/provision.go` (Soul base rules)**:
  - Removed anti-planning instructions: deleted "不要停下來問「要開始嗎」，也不要寫「接下來去做某某」就停下來"
  - Added positive planning instruction: "多步驟工作：先說一行計畫（「我會 A → B → C」），做的時候只在關鍵決定、遇到障礙、改變計畫時簡短報一句，完成時給結論"
  - Retained "decide and proceed" principle: "範圍不清楚但能合理假設時，先照假設做，把假設寫在回覆裡；只有花錢、對外發送、刪除，或真的沒辦法開始時才停下來問"

**Section 3: Runtime Structure (Partial)** 🟡
- **`internal/runtime/conclusion.go` (new)**: 
  - Implemented `conclusionCheck()` heuristic detection function
  - Checks for conclusion indicators: "完成", "做好", "已經", "/workspace", "不用"
  - Checks against incomplete indicators: "接下來", "然後", "正在", "等待"
  - Confidence scoring system (threshold 0.25)
  - 8 test cases covering various conclusion/incomplete scenarios

- **`internal/runtime/conclusion.go` (new)**: 
  - Added `RequestConclusion()` helper for requesting summary from engine

### Remaining Work (Sections 3-5):

**Section 3: Runtime Structure (Integration)** 🔲
- Integrate `conclusionCheck` into `runtime.finish()` to detect missing conclusions
- Request summary from engine when conclusion absent
- Implement redundant tool call detection (`internal/runtime/progress.go`)
- Implement no-progress turn detection in watchdog
- Add bot<->bot goal+done-criteria enforcement in `internal/collab/room.go`

**Section 4: Length Calibration (Full System)** 🔲
- Create `internal/runtime/replyclass.go` with classification logic
- Implement post-check rewriter for outlier lengths
- Integrate into runtime flow

**Section 5: Offline Eval Harness** 🔲
- Create `scripts/eval` with fake/recorded engine
- Implement 6 test scenarios:
  1. Time-to-plan measurement
  2. Turns-to-conclusion tracking
  3. No-progress turn ratio
  4. Conclusion presence validation
  5. Reply length vs class deviation
  6. Redundant call detection
- Collect before/after metrics

---

## 9. Expected Impact

### Before vs After (Qualitative):

| Aspect | Before | After (Sections 1-2 Complete) |
|--------|--------|-------------------------------|
| Planning | Jumps straight to action | States one-line plan first for multi-step work |
| Progress reporting | Every tool call feels like progress | Only key decisions, blockers, plan changes |
| Conclusions | Often stops mid-work | Requires conclusion: what's done, what's left, next step |
| Reply length | Hard caps (3-5 sentences, 2 paragraphs) causing truncation | Adaptive sizing: chitchat→quick→task→deep |
| Brevity approach | "Don't write telegrams" (negative) | "Fewer ideas, fully explained" (positive) |
| Decide & proceed | Conflicting instructions ("don't ask" vs implicit asking) | Clear: decide unless consequential (money/delete/send) |

### Metrics to Track (Section 5):

Once eval harness is implemented:
- **Time-to-plan**: Expect <200 chars before plan appears (currently: varies widely)
- **Turns-to-conclusion**: Expect <3 turns for simple tasks (currently: 4-6 common)
- **No-progress ratio**: Target <10% (currently: unknown baseline)
- **Conclusion present**: Target 100% of completed tasks (currently: ~60-70% estimated)
- **Length deviation**: Target σ<100 chars within class range (currently: large variance)
- **Redundant calls**: Target 0% (currently: observed in failed tool scenarios)

---

**Document Version**: 1.1  
**Created**: 2026-10-22  
**Updated**: 2026-10-22 (implementation status added)  
**By**: Cloud Agent analyzing and fixing XBot busy-work issues
