# 固定限制值對照表（X01）

所有固定數字都在 `internal/limits`。這份表列出每個常數的值、出處，以及和 PRD 不一致的地方。

- **權威順序**：數字以 LazyBoy DEV 程式（`/home/daniel/Projects/LazyBoyHermes`）為準。PRD 和程式不一致時照程式做，PRD 已改成程式值並註明「（以 LazyBoy DEV 程式為準）」。
- **出處欄**：`檔案:行號`，路徑相對於 LazyBoy 根目錄。寫「XBot 新增」的是 LazyBoy 沒有、XBot 自己定的。
- **不一致欄**：「—」表示一致；有內容的表示 PRD 原本寫的和程式不同（或 PRD 有、程式沒有）。
- 測試 `internal/limits/limits_test.go` 會檢查關鍵數字沒被誤改。改數字前先確認 LazyBoy 程式真的改了，再同步更新這份表。

## 1. Bot 之間傳話（`collab.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `MaxHops` | 6 | 次 | server/collab.py:30 `MAX_HOPS` | §12、§18、LP-* | — （結果回傳 relay 會加 hop 但不擋） |
| `BotMessageMax` | 12,000 | 字元 | server/collab.py:64 `MESSAGE_MAX` | §12、§18、AA-8 | **有**：PRD §18 原寫「超過時一律存檔＋預覽」。程式只有「交回的結果」存檔；Bot 主動傳話和你在群組的發言是拒絕；Bot 開群組或在群組發言是截斷。PRD 已更正 |
| `ResultPreview` | 3,000 | 字元 | server/collab.py:64 `RESULT_PREVIEW` | §12、§18、AA-8 | — |
| `CarryFilesMax` | 10 | 個 | server/collab.py:62 `CARRY_FILES` | §12、§18、AA-7 | **有**：PRD §18 原寫「超過時拒絕並說明」。程式是訊息照送、超出的檔案略過不複製、不報錯。PRD 已更正 |
| `CarryBytesMax` | 50 MiB | 位元組 | server/collab.py:62 `CARRY_BYTES` | §12、§18、AA-7 | 同上（會超過的那個檔案略過，後面較小的仍可能被複製） |
| `SilentTailMax` | 40 | 字元 | server/collab.py:56 `is_silent` | AA-4 | PRD 沒寫：`[silent]` 後面 40 字內仍算不發言 |
| `RosterAboutMax` | 160 | 字元 | server/collab.py:124 `directory` | AA-1 | — |

## 2. 群組（`collab.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `RoomMembersMin` | 2 | 個 Bot | server/collab.py:40 `ROOM_MIN` | §12、§18、RM-1 | — |
| `RoomMembersMax` | 10 | 個 Bot | server/collab.py:40 `ROOM_MAX` | §12、§18、RM-1 | — （LazyBoy collab.py 開頭說明寫「two to six」，程式是 10，以程式為準） |
| `RoomRounds` | 4 | 輪 | server/collab.py:41 `ROOM_ROUNDS`（:495、:550） | §12、§18、LP-3 | — |
| `RoomBacklog` | 20 | 則 | server/collab.py:468 `room_prompt` | RM-4 | — |
| `RoomNameMax` | 60 | 字元 | server/collab.py:379 `create_room` | — | PRD 沒寫 |

## 3. Bot 建立 Bot（`collab.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `BotCreatesPerDay` | 3 | 個／24 小時 | server/collab.py:32 `BOTS_PER_DAY` | §12、§18、LP-9 | — （PRD §12 寫「每日」，程式是滾動 24 小時） |
| `ManagerBotCreatesPerDay` | 20 | 個／24 小時 | server/collab.py:34 `MANAGER_BOTS_PER_DAY`；org.py:319 | §12、§18、LP-9 | — （建團隊一律用 20，不看建立者有沒有管理權限） |
| `BotCreateWindow` | 24 | 小時 | server/collab.py:564 `create_bot` | §18 | — |
| `BotsMax` | 50 | 個 | server/collab.py:35 `MAX_BOTS` | §12、§18、LP-9 | — （PRD §12「Bot 總數」和「全部 Bot」重複列了兩次） |
| `BotCreateNameMax` | 40 | 字元 | server/collab.py:568 `create_bot` | — | PRD 沒寫（你自己建 Bot 的上限是 `BotNameMax` 100） |
| `BotProfileTitleMax` | 60 | 字元 | server/collab.py:572、:583 | — | PRD 沒寫 |

## 4. 團隊與核定（`collab.go`、`botmcp.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `TeamMax` | 10 | 位（含主管） | server/org.py:32 `TEAM_MAX` | §12、§18 | — |
| `TeamNameMax` | 40 | 字元 | server/org.py:305 `create_team` | — | PRD 沒寫 |
| `TeamRecentDays` | 7 | 天 | server/org.py:33 `RECENT_DAYS` | — | PRD 沒寫 |
| `ApprovalPendingMax` | 10 | 件／Bot | server/org.py:241 `request_approval` | §12、§18、TM-4 | — |
| `ApprovalSubjectMax` | 120 | 字元 | server/org.py:236 | — | — |
| `ApprovalDetailsMax` | 4,000 | 字元 | server/org.py:244 | — | — |
| `ApprovalCategoryMax` | 40 | 字元 | server/org.py:245 | — | — |
| `ApprovalItemsMax` | 20 | 項 | server/org.py:230 `set_approval_items` | §18 | — |
| `ApprovalItemMax` | 40 | 字元／項 | server/org.py:230 | §18 | — |
| `ApprovalNoteMax` | 2,000 | 字元 | server/org.py:264 `decide` | — | PRD 沒寫 |
| `DeleteReasonMax` | 500 | 字元 | server/org.py:414 `request_delete_bot` | — | PRD 沒寫 |
| `CardTitleMax`／`CardBodyMax` | 120／4,000 | 字元 | XBot 新增（同核定主旨、說明） | — | — |
| `CardFilesMax` | 10 | 個 | 同 `CarryFilesMax` | AA-7 | — |
| `CardPathMax` | 1,024 | 字元 | XBot 新增 | — | — |

## 5. 提案與討論（`collab.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `AgendaOpenMax` | 3 | 件／Bot（提案＋討論合計） | server/agenda.py:28 `MAX_OPEN` | §12、§18 | — |
| `ProposalDaysMin`／`Max` | 1／14 | 天 | server/agenda.py:29 `MIN_DAYS, MAX_DAYS` | TM-5 | PRD 原本只寫「N 天」；已補上範圍 |
| `ProposalDaysDefault` | 3 | 天 | server/agenda.py:87 `submit(days=3)` | TM-5 | 同上 |
| `VetoMemory` | 14 | 天 | server/agenda.py:37 `VETO_MEMORY` | §12、§18 | — |
| `DiscussLeadMin` | 30 | 分鐘 | server/agenda.py:111、:149 | TM-5 | **有**：PRD TM-5 原寫 `request_discussion`「每週 ≤ 1 次」。程式沒有每週次數限制，只有 30 分鐘後到 30 天內、和提案合計 3 件。PRD 已更正 |
| `DiscussAheadMax` | 30 | 天 | server/agenda.py:30 `DISCUSS_MAX_DAYS` | TM-5 | 同上 |
| `DiscussDefaultHour` | 18 | 時（台北） | server/agenda.py:38 `DISCUSS_HOUR` | TM-5 | 同上 |
| `ProposalGrace` | 30 | 分鐘 | server/agenda.py:32 `GRACE` | — | PRD 沒寫 |
| `ProposalGraceExtra` | 1 | 天 | server/agenda.py:33 `GRACE_EXTRA` | — | PRD 沒寫 |
| `AgendaDetailsMax` | 4,000 | 字元 | server/agenda.py:116 | — | — |
| `AgendaNoteMax` | 2,000 | 字元 | server/agenda.py:138 | — | — |
| `ReviewDaysDefault`／`Max` | 7／30 | 天 | server/agenda.py:44 `RECENT_DAYS`、:205 | — | — |
| `CalendarDenseInterval` | 180 | 分鐘 | server/agenda.py:42 `DENSE_INTERVAL` | RT-5 | — |
| `CalendarWeekItems` | 120 | 項 | server/agenda.py:43 `WEEK_ITEMS` | RT-5 | — |
| `AgendaZone` | Asia/Taipei | 時區 | server/agenda.py:45 `ZONE` | TM-5 | — |
| `AgendaSubjectMax` | 120 | 字元 | server/agenda.py:90 `plain(subject, 120)` | — | — |
| `AgendaTick` | 30 | 秒 | server/scheduling.py:293 `loop`（每 2 秒） | — | **有**：XBot 改成 30 秒（到期時間以分鐘計）（X03） |

## 6. 執行中的工作：看門狗、重試、自動續做（`collab.go`、`inbox.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `WatchdogIdle` | 15 | 分鐘 | server/runtime.py:553 `IDLE_LIMIT` | §12、§19 | — |
| `WatchdogToolIdle` | 40 | 分鐘 | server/runtime.py:554 `TOOL_IDLE_LIMIT` | §12、§19 | — |
| `StopGrace` | 60 | 秒 | server/runtime.py:555 `STOP_GRACE` | — | PRD 沒寫 |
| `WatchInterval` | 5 | 秒 | server/runtime.py:556 `WATCH_INTERVAL` | — | PRD 沒寫 |
| `QuestionTTL` | 10 | 分鐘 | server/runtime.py:1024 `QUESTION_TTL` | §12、§18、LP-7 | — |
| `UndoWindow` | 10 | 分鐘 | XBot 新增（PRD MCP-10） | MCP-10 | LazyBoy 沒有 |
| `ProviderRetryDelays()` | 2／10／30 | 分鐘（3 次） | server/runtime.py:670 `RETRY_DELAYS` | §12、§19 | — |
| `AutoContinueMax` | 2 | 次 | server/runtime.py:696 `AUTO_CONTINUE` | §12、§19 | — |
| `StallNudgesMax` | 2 | 次／背景子任務 | server/runtime.py:709 `STALL_NUDGES` | §19 | — |
| `RestartResumeMax` | 1 | 次／工作 | XBot 新增（服務重開時做到一半的工作自動接著做） | — | LazyBoy 標成 interrupted、不重播 |
| `StrandedWindow` | 3 | 小時 | server/runtime.py:715 | — | PRD 沒寫 |
| `PromiseMaxChars` | 70 | 字元 | server/runtime.py:736 | §19 | — |
| `HandBackTailChars` | 300 | 字元 | server/runtime.py:747 | — | PRD 沒寫 |
| `ProgressQuestionMax` | 60 | 字元 | server/runtime.py:631 | §19.2 | — |
| `StreamDeltaInterval` | 60 | 毫秒 | server/runtime.py:942 `DELTA_INTERVAL` | — | — |
| `StreamPersistInterval` | 500 | 毫秒 | server/runtime.py:552 `PERSIST_INTERVAL` | — | — |
| `TaskStepsKept`／`TaskPlanMax`／`TaskWorkersKept` | 60／30／12 | 筆 | server/runtime.py:879、:850、:913 | — | — |
| `FailureDetailMax` | 1,500 | 字元 | server/runtime.py:545 | — | — |
| `ChatTextMax` | 64,000 | 字元 | server/runtime.py:333 `_submit` | §12 | 和 PRD 一致；**XBot 原本的常數錯了**（見第 10 節） |
| `AnswerTextMax` | 64,000 | 字元 | XBot 新增（同 `ChatTextMax`） | — | — |
| `StrandedGoalKey` | 80 | 字元 | server/runtime.py:722（`goal[:80]`） | — | PRD 沒寫（X04） |
| `JudgeTaskDoneWait` | 1 | 秒 | XBot 新增（PRD §20.2 用途 2 等待 1 秒） | §20 FJ-2 | —（X04，`judge.go`） |
| `JudgeReasonMax`／`JudgeAskMax`／`JudgeNoteMax` | 120／300／500 | 字元 | XBot 新增 | §20 FJ-1、FJ-7 | —（X04，`judge.go`） |

## 7. 定時任務（`routines.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `RoutinesPerBot` | 50 | 個 | server/scheduling.py:26 `MAX_ROUTINES` | §12 | — |
| `RoutineIntervalMin` | 5 | 分鐘 | server/scheduling.py:27 `MIN_INTERVAL` | RT-1 | — |
| `RoutineIntervalMax` | 7 | 天 | server/scheduling.py:28 `MAX_INTERVAL` | — | PRD 沒寫上限 |
| `RoutineJitterMaxMinutes` | 240 | 分鐘 | server/scheduling.py:31 `MAX_JITTER` | §12 | — （間隔型須小於間隔：:92） |
| `RoutineJitterChoices()` | 15／30／60／90／120 | 分鐘 | web/src/routines.tsx:148 | §12 | — |
| `RoutineStartRetryDelays()` | 1／5／15／60 | 分鐘 | server/scheduling.py:24 `RETRY_MINUTES` | §19 | — |
| `RoutineHistory` | 20 | 筆 | server/scheduling.py:32 `HISTORY` | — | — |
| `RoutineHookSpacing` | 60 | 秒 | server/scheduling.py:34 `HOOK_SPACING` | §18 | — |
| `RoutineHookBytes` | 64 KiB | 位元組 | server/scheduling.py:35 `HOOK_BYTES` | — | — |
| `RoutineHookTextMax` | 8,000 | 字元 | server/scheduling.py:215 | — | — |
| `GoalRoundsDefault`／`GoalRoundsMax` | 10／50 | 輪 | server/scheduling.py:41 | §12、§19 | — |
| `GoalDoneWhenMax`／`GoalSummaryMax` | 1,000／4,000 | 字元 | server/scheduling.py:238、:258 | — | — |
| `RoutineTitleMax` | 80 | 字元 | server/scheduling.py:188 | — | — |
| `RoutineErrorMax` | 500 | 字元 | server/scheduling.py:284 | — | — |
| `RoutineTick` | 2 | 秒 | server/scheduling.py:293 | — | — |

## 8. 上傳、檔案、教學（`files.go`）

| 常數 | 值 | 單位 | LazyBoy 出處 | PRD | 不一致 |
|---|---|---|---|---|---|
| `UploadFilesMax` | 10 | 個 | server/main.py:283 `UPLOAD_LIMITS` | — | — |
| `UploadFileBytes` | 50 MiB | 位元組 | server/main.py:283 | — | — |
| `UploadImageBytes` | 20 MiB | 位元組 | server/main.py:283 | — | — |
| `UploadTotalBytes` | 200 MiB | 位元組 | server/main.py:283 | — | — |
| `AvatarPNGBytes` | 8 MiB | 位元組 | server/main.py:891 | — | — |
| `FolderEntries` | 300 | 項 | server/main.py:681 `FOLDER_ENTRIES` | — | — |
| `FileNameMax` | 180 | 字元 | server/files.py:18 `filename` | — | — |
| `FilesScanMax` | 500 | 個 | server/runtime.py:1185 `scan_files` | — | — |
| `FilesFolderScanMax`／`FilesWalkScanMax` | 10,000／20,000 | 項 | XBot 新增 | — | — |
| `FilesListOutputBytes` | 8 MiB | 位元組 | XBot 新增 | — | — |
| `FilePathDepth`／`FilePathMax` | 32 層／1,024 字元 | — | XBot 新增 | — | — |
| `FilePreviewBytes`／`FilePreviewBytesMax` | 256 KiB／1 MiB | 位元組 | XBot 新增 | — | — |
| `FilePreviewRows`／`FilePreviewColumns` | 1,000／100 | 列／欄 | XBot 新增（E07 大檔只預覽前 1,000 列） | — | — |
| `FileSheetBytes`／`FileSheetXMLBytes` | 20／64 MiB | 位元組 | XBot 新增 | — | — |
| `ThumbSize`／`ThumbPixelsMax` | 320 像素／5,000 萬像素 | — | XBot 新增 | — | — |
| `FileWriteTextMax` | 512 KiB | 位元組 | XBot 新增 | — | — |
| `ArchiveEntries`／`ArchiveCountLimit` | 2,000／100,000 | 項 | server/previews.py:20、:22 | — | — |
| `TeachStepsMax` | 300 | 步 | server/teaching.py:16 `MAX_STEPS` | — | — |
| `TeachStartTimeout`／`TeachStopTimeout` | 15／6 | 秒 | server/teaching.py:159、:160 | — | — |
| `TeachGoalMax`／`TeachNoteMax` | 200／500 | 字元 | server/teaching.py:203、:254 | — | — |
| `TeachPoll` | 1 | 秒 | server/teaching.py `watch()` | — | — |
| `TeachSubmitMerge`／`TeachNavigateMerge`／`TeachTabMerge`／`TeachCauseWindow` | 1.5／2.5／4／5 | 秒 | server/teaching.py `normalize()` | — | — |
| `TeachKeyShots` | 8 | 張 | server/teaching.py `key_shots(limit=8)` | — | — |
| `TeachShotBytes`／`TeachRowsBytes` | 2／8 MiB | 位元組 | XBot 新增（B32） | — | — |

## 9. 先前任務卡的常數（已逐一對過 LazyBoy 程式）

| 常數 | 值 | LazyBoy 出處 | 結果 |
|---|---|---|---|
| `BotNameMax` | 100 字元 | server/main.py:100 | 正確 |
| `BotDescriptionMax`／`BotSoulMax` | 1,000／4,000 字元 | server/org.py:386 | 正確 |
| `BoxCPUs`／`BoxCPUPeriod`／`BoxMemoryBytes`／`BoxShmBytes` | 2／10 ms／3 GiB／512 MiB | server/computers.py:49 `RESOURCES` | 正確 |
| `BoxStopTimeout` | 20 秒 | server/computers.py:380 | 正確 |
| `BoxReadyTimeout`／`BoxReadyPoll` | 75 秒／0.6 秒 | server/computers.py:396、:404 | 正確 |
| `BoxProbeTimeout` | 2 秒 | server/computers.py:341 | 正確 |
| `BoxIdleMinutes` | 30 分鐘 | 無（PRD BX-12） | LazyBoy 程式沒有休眠 |
| `BoxIdleCheckInterval` | 1 分鐘 | XBot 新增（B13）：閒置休眠多久檢查一次 | — |
| `BoxTmpfsTmpBytes`／`BoxTmpfsRunBytes`／`BoxTmpfsVarTmpBytes` | 1 GiB／64 MiB／256 MiB | XBot 新增（B13，SEC-3 唯讀根檔案系統下可寫的 tmpfs）；LazyBoy 根目錄可寫、沒有上限 | — |
| `NetguardApplyTimeout` | 30 秒 | XBot 新增（B13，SEC-4）：套用網路防護規則的上限 | — |
| `NetAllowMax` | 32 筆 | XBot 新增（B13，SEC-4）：每個 Bot 的私有網路白名單上限 | — |
| `BoxMoveKeep`／`BoxMoveCleanupEvery` | 24 小時／10 分鐘 | XBot 新增（B31，CL-5）：搬家後原主機上的舊資料保留多久、多久檢查一次 | — |
| `BoxMoveHelperTimeout`／`BoxMoveTimeout`／`BoxMoveProgressEvery`／`BoxMoveHistory`／`BoxMoveManifestBytes` | 30 分鐘／6 小時／0.5 秒／20 筆／256 MiB | XBot 新增（B31）：量資料與驗證的輔助容器上限、一次搬家上限、進度事件間隔、紀錄筆數、驗證清單大小 | — |
| `CleanupScratchAge`／`CleanupUploadAge` | 3 天／30 天 | server/cleanup.py `SCRATCH_DAYS`、`UPLOAD_DAYS` | 正確；XBot 的 `/workspace/tmp` 也用 3 天 |
| `CleanupBrowserCacheBytes` | 200 MiB | server/cleanup.py `BROWSER_CACHE_LIMIT` | 正確 |
| `CleanupFirstDelay`／`CleanupEvery` | 120 秒／24 小時 | server/cleanup.py `daily()` | 正確 |
| `CleanupLiveCacheTimeout`／`CleanupLiveCacheChecks`／`CleanupLiveCachePoll` | 30 秒／10 次／1 秒 | server/cleanup.py `clear_browser_cache()` | 正確 |
| （`PREVIEW_DAYS` 7 天） | — | server/cleanup.py `PREVIEW_DAYS` | XBot 沒有預覽快取（Office 檔不轉 PDF，只能下載），沒有這一項 |
| `CleanupBotTimeout`／`CleanupHelperTmpBytes` | 10 分鐘／1 MiB | XBot 新增（BX-14）：一個 Bot 的清理或量用量上限（含沒開機時的輔助容器）、輔助容器的 /tmp | — |
| `BackupKeepTimeout`／`BackupHostWait` | 6 小時／20 秒 | XBot 新增（V10）：還原前在主機上複製保留原本電腦資料的上限、備份與還原等遠端主機連上的上限 | — |
| `EgressImage`／`EgressProxyPort`／`EgressMemoryBytes`／`EgressStatusTimeout`／`EgressPullTimeout` | tailscale/tailscale:v1.88.3／1055／256 MiB／10 秒／10 分鐘 | XBot 新增（B31，CL-7）：「從家裡出去」的 Tailscale 小容器 | — |
| `HostImageCacheTTL` | 15 秒 | XBot 新增（B13）：主機清單比對映像是否過期時，本機映像資訊的快取 | — |
| `HermesReadyTimeout`／`HermesCallTimeout` | 15／90 秒 | server/rpc.py:25、:31 | 正確 |
| `HermesPingInterval`／`HermesPingTimeout`／`HermesMaxFrameBytes` | 15 秒／30 秒／16 MiB | server/rpc.py:21 | 正確 |
| `HermesMaxIterations`／`HermesTerminalTimeoutSeconds` | 200／120 秒 | server/computers.py:213、:202 | 正確 |
| `HermesPruneTokens` | 48,000 | server/computers.py:222 | 正確 |
| `HermesCompressTokens` | **72,000** | server/computers.py:223 是 100,000 | **刻意不同**（見 §10）；舊版寫進 config.yaml 的 100,000（`HermesCompressTokensOld`）開機前換成新值，使用者改過的保留 |
| `HermesContextLength`／`HermesSkillNudgeInterval`／`HermesReviewInputTokens`／`HermesDelegationMaxIterations` | 200,000／40 次／150,000／60 次 | XBot 新增（2026-10-19 省 token）：Hermes 的 `model.context_length`、`skills.creation_nudge_interval`、`auxiliary.background_review.max_input_tokens`、`delegation.max_iterations`；LazyBoy 沒有設定（用 Hermes 預設 自動偵測／10／視窗 75%／250） | — |
| `HermesIdleWait` | 30 秒 | XBot 新增：送出前等 Hermes 收完上一輪的尾（`session.info` running=false）；LazyBoy 兩輪之間有路由與設定呼叫，碰不到這個時間差 | — |
| `HermesStartTimeout` | 630 秒 | Hermes tui_gateway/server.py `_agent_build_wait_cap`（600 秒）＋30 秒：送出後等 `message.start` 的上限 | — |
| `SessionTTL` | 30 天 | server/main.py:192 | 正確 |
| `DBBusyTimeout` | 5 秒 | server/store.py:37 | 正確 |
| `EventsKeepPerBot` | 5,000 筆 | server/store.py:221 | 正確 |
| `ModelUsageJSONBytes` | 4 MiB | server/usage.py:12 | 正確 |
| `ModelConnectTimeout`／`ModelResponseHeaderTimeout`／`ModelStreamIdleTimeout` | 15／300／300 秒 | server/main.py:1264 | 正確 |
| `ModelMaxConns`／`ModelMaxIdleConns`／`ModelIdleConnTimeout` | 100／32／60 秒 | server/main.py:1265 | 正確 |
| `ModelProbeTimeout`／`ModelProbeDetailChars` | 20 秒／400 字元 | server/model_check.py:123、:118–120 | 正確 |
| `UsageSummaryMaxDays`／`UsageSummaryDefaultDays` | 365／30 天 | server/usage.py:106、:104 | 正確 |
| `DefaultGlobalMonthlyMicros` | 20 美元 | XBot 新增（V10）：全部 Bot 合計的預設每月花費上限；新安裝直接套用，舊安裝沒設上限時套用一次並發通知卡 | — |
| `ChatTextMax` | 64,000 字元 | server/runtime.py:333 | **已更正**（見下） |

其他常數（MCP 配對、登入限速、SSE、ACP、OAuth、分頁等）註明為「XBot 新增」或出自 OpenMausBot／Hermes，不在 LazyBoy 對照範圍。

### 技能與記憶（C31，`skills.go`）

| 常數 | 值 | 出處 | 結果 |
|---|---|---|---|
| `MemoryCharsDefault`／`MemoryUserCharsDefault` | 2,200／1,375 字 | server/memory.py `TARGETS`；Hermes agent/agent_init.py | 正確 |
| `MemoryEntryMax` | 2,000 字 | server/memory.py `ENTRY_LIMIT` | 正確 |
| `SkillLibraryResults`／`SkillLibraryText` | 8／60,000 字 | server/skills.py `LIBRARY_RESULTS`、`LIBRARY_TEXT` | 正確 |
| `SkillNameMax`／`SkillDescriptionMax`／`SkillContentMax`／`SkillFileBytes` | 64／1,024／100,000 字／1 MiB | Hermes tools/skill_manager_tool.py（LazyBoy 內容上限 200,000，比 Hermes 能讀的大，照 Hermes） | 照 Hermes |
| `SkillImportBytes`／`SkillImportFilesMax`／`SkillTreeBytes` | 8 MiB／64 個／64 MiB | XBot 新增 | — |
| `MemoryEntriesMax`／`MemoryVersionsKept` | 500 條／20 版 | XBot 新增（PRD MM-3 只說可復原） | — |
| `MemoryWatchQueue` | 256 個事件 | XBot 新增（Bot 用記憶工具後馬上記下變更；滿了就丟，打開記憶頁時補記） | — |

PRD MM-1 的「每回合載入前 200 行／24 KB」是 OpenMausBot 的做法；Hermes 實際是兩份清單各有字數上限、每個對話開始時載入固定快照（tools/memory_tool_store.py），照 Hermes。

## 10. 這次更正的既有常數

| 常數 | 原值 | 新值 | 原因 |
|---|---|---|---|
| `ChatTextMax`（連帶 `AnswerTextMax`、`chat.send` 與 `chat.answer` 的 `max` 標籤） | 100,000 | **64,000** | 原註解說「LazyBoy 對使用者的訊息沒有設上限」，但 server/runtime.py:333 有 `len(text)>64000` 就拒絕；PRD §12 也寫 64,000 |
| `CardFilesMax` | 10（獨立數字） | `= CarryFilesMax` | 值不變，改成直接引用傳話帶檔上限 |
| `HermesCompressTokens` | 100,000 | **72,000** | 和 LazyBoy 不同（2026-10-19）。實測水哥哥一天 20.9M 輸入 tokens：Hermes 經 Model Proxy 量不到視窗、把 deepseek-v4-flash 當成 1M，每次請求停在 86–100K 幾乎不摘要。同時把 `model.context_length` 定成 200,000（`HermesContextLength`），72K 才真的會摘要、摘要後的尾巴從 25K 變 10K |

既有常數的註解都補上了 LazyBoy 行號。

## 11. PRD 有、LazyBoy 程式沒有的限制

| PRD 項目 | PRD 值 | 說明 |
|---|---|---|
| §12、LP-4、BL-2 委派樹 | 深度 4／節點 24／執行 48 | LazyBoy 規劃值，程式沒有。XBot 補強（P1），還沒有常數 |
| §12 每 Bot 進行中對話 | 3 | LazyBoy 程式找不到對應限制（Hermes 的 `delegation.max_concurrent_children` 預設是 10，也不是 3）。**待確認**，還沒有常數 |
| §12 Box 閒置休眠 | 30 分鐘 | LazyBoy 程式沒有休眠，XBot 依 BX-12 新增（`BoxIdleMinutes`） |
| §19 隊友沒動靜回報主管／沒回覆提醒 | 20／30 分鐘 | LazyBoy 規劃 X-TEAM-4／6，程式沒有；還沒有常數 |
| BL-3、LP-8 每條工作鏈的 token 預算 | 不限（`limits.ChainBudgetTokens = 0`） | XBot 新增；2026-10-19 使用者決定取消 |
| MCP-10 代為核准可復原 | 10 分鐘 | XBot 新增（`UndoWindow`） |

## 12. 給 X02～X04 的提醒

- **群組裡 Bot 的回覆沒有長度上限**：LazyBoy `room_answer`（collab.py:486）直接把結果貼進群組，沒有套 `MESSAGE_MAX`。照程式翻譯會保留這個行為；要不要截斷是待確認事項。
- **hop 的計算**：每送一次 hop＋1（collab.py:191），只有 `relay=True`（交回結果，collab.py:192）可以超過 `MaxHops`。
- **回合數**：使用者發言是第 0 輪；Bot 回覆後喚醒別人是第 1 輪起，`round > RoomRounds` 就不再喚醒（collab.py:495）；Bot 在同一個群組回合裡用 `post_to_group` 會接續計數（collab.py:549–550）。
- **建立 Bot 的「每天」**是建立時間在過去 24 小時內的筆數（滾動），不是日曆日。
