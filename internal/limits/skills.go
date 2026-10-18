package limits

// 技能與記憶（C31，PRD SK-*、MM-*）。Hermes 的值出自映像裡的 /opt/hermes（LazyBoy vendor/hermes-agent 同一版）。

const (
	// SkillNameMax 是技能名稱的長度（字）。Hermes tools/skill_manager_tool.py MAX_NAME_LENGTH。
	SkillNameMax = 64

	// SkillDescriptionMax 是技能「什麼時候使用」的長度（字）。Hermes tools/skill_manager_tool.py MAX_DESCRIPTION_LENGTH。
	SkillDescriptionMax = 1024

	// SkillContentMax 是一個 SKILL.md 的長度（字）。Hermes tools/skill_manager_tool.py MAX_SKILL_CONTENT_CHARS
	// （LazyBoy server/skills.py 用 200,000，比 Hermes 自己能讀的還大，這裡照 Hermes）。
	SkillContentMax = 100_000

	// SkillFileBytes 是技能附帶的一個檔案（scripts/、references/…）的大小上限。Hermes MAX_SKILL_FILE_BYTES（1 MiB）。
	SkillFileBytes int64 = 1 << 20

	// SkillImportBytes 是匯入一個技能（.md 或 .zip）的大小上限。XBot 新增：Hermes 一個技能最多 SKILL.md＋幾個 1 MiB 的檔案。
	SkillImportBytes int64 = 8 << 20

	// SkillImportFilesMax 是一個匯入的 .zip 最多幾個檔案。XBot 新增。
	SkillImportFilesMax = 64

	// SkillTreeBytes 是讀一個 Bot 全部技能（/hermes/skills）時的大小上限。XBot 新增：
	// Hermes 內建的技能約 4 MB，留十幾倍給 Bot 自己學的技能。
	SkillTreeBytes int64 = 64 << 20

	// SkillLibraryResults 是 Bot 搜尋技能庫一次最多回幾個。LazyBoy server/skills.py LIBRARY_RESULTS。
	SkillLibraryResults = 8

	// SkillLibraryText 是 Bot 載入一個技能庫技能時最多給幾個字。LazyBoy server/skills.py LIBRARY_TEXT。
	SkillLibraryText = 60000

	// MemoryCharsDefault、MemoryUserCharsDefault 是 Bot 記住的事（MEMORY.md）與關於你（USER.md）的上限（字），
	// Bot 的 config.yaml 沒有設 memory.memory_char_limit／user_char_limit 時用。
	// Hermes agent/agent_init.py（2200、1375）；LazyBoy server/memory.py TARGETS；design/specs/G06-Skills.json。
	MemoryCharsDefault     = 2200
	MemoryUserCharsDefault = 1375

	// MemoryEntryMax 是一條記憶的長度（字）。LazyBoy server/memory.py ENTRY_LIMIT。
	MemoryEntryMax = 2000

	// MemoryEntriesMax 是一份記憶最多幾條（操作輸入的上限）。XBot 新增：字數上限 2,200 已經限制了總量，這只擋異常的輸入。
	MemoryEntriesMax = 500

	// MemoryVersionsKept 是每個 Bot 每份記憶在資料庫保留幾個舊版本（復原用，MM-3）。XBot 新增：
	// PRD 只說「變更日誌可復原」；20 版約是幾週的修改，一份記憶最多 2,200 字，佔用很小。
	MemoryVersionsKept = 20
)
