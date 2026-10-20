---
name: grok-build
description: 較大的寫程式工作（多個檔案的專案、要跑測試的修 bug、重構、審查程式碼），而且電腦裡的 Grok Build CLI（grok）已經登入時，交給它在 /workspace 實作。
---

# Grok Build 寫程式

只有兩件事都成立才交給電腦裡的 `grok`（xAI Grok Build）：

1. 是較大的寫程式工作：多個檔案的專案、要跑測試才知道對不對的修 bug、重構、看 PR。
   單一檔案的小網頁、小腳本、整理資料，**自己用 write_file／terminal 做**，比交辦快。
2. `grok` 已經登入（下面第 1 步回 `ready`）。

不成立就自己做完，不要停下來請使用者登入、也不要請使用者接手電腦。做完後可以用一句話提：「電腦裡的 Grok 登入後，大的程式工作可以交給它」。

## 1. 確認可用

```
terminal(command="command -v grok >/dev/null && test -s ~/.grok/auth.json && echo ready || echo not-ready")
```

`not-ready`：照上面，自己做。

## 2. 交辦

每個專案一個資料夾：`/workspace/projects/<簡短英文名>`，不存在就建立並 `git init`。把需求寫成一段完整的 prompt：目標、技術限制、驗收方式（要跑哪些測試或指令）。

一次性工作（預設）：

```
terminal(command="grok --no-auto-update --always-approve --output-format json -p '<需求，含驗收方式；完成後執行測試並摘要改了什麼>'", workdir="/workspace/projects/<name>", timeout=600)
```

超過 10 分鐘的大工作改背景執行並追蹤：

```
terminal(command="grok --no-auto-update --always-approve -p '<需求>'", workdir="/workspace/projects/<name>", background=true, notify_on_complete=true)
```

接續同一個專案的修改用 `-c`（延續該資料夾最近的 session）：

```
terminal(command="grok --no-auto-update --always-approve -c -p '<補充修改>'", workdir="/workspace/projects/<name>", timeout=600)
```

只要審查、不准改檔時拿掉 `--always-approve`，並要求「只輸出 Markdown 結論」。

grok 回報登入失效或額度用完時，改成自己做，不要停下來等。

## 3. 驗收與回報

- 自己再跑一次關鍵指令（測試、建置或啟動），確認 grok 說的是真的。
- 失敗時把錯誤訊息原文用 `-c -p` 交回 grok 修，同一錯誤最多兩輪，之後自己修或向使用者說明卡在哪。
- 回覆只寫：完成了什麼、怎麼執行或打開、還沒做的部分，以及專案路徑。不要貼整段程式碼或 grok 的完整輸出。
