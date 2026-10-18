# XBot 內建技能 — 來源與授權

這個資料夾的技能（`deep-research`、`grok-build`、`self-improve`）原封不動複製自 LazyBoyHermes
`server/builtin_skills/`（同一位作者的專案，PRD SK-3），隨 XBot 一起散佈。

- 每個 Bot 的電腦第一次開機前放進 `/hermes/skills/<名稱>/SKILL.md`，只放一次：
  使用者之後修改或刪除都會保留（`/hermes/skills/.xbot.json` 的 `seeded` 記下放過哪些）。
- 要更新內建技能時，從 LazyBoyHermes 複製新版過來，並在這裡註記日期與來源版本。
- `self-improve` 提到的 `propose`、`request_discussion` 是 LazyBoy 的產品工具；XBot 對應的提案與討論操作在 X03 任務卡。

LazyBoyHermes 沒有另外的授權檔；這些技能和 XBot 由同一位作者擁有，以 XBot 的授權散佈。
複製日期：2026-10-18。
