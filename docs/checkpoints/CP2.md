# CP2：雲端主機連線原型（B11，決策點）

> 結論：**照原設計繼續**。一條 SSH 連線就能同時管 Docker、看畫面（noVNC）、讓 Bot 經 product.sock 反向連回 XBot 拿模型與工具。
> Git：tag `cp2`。完整數據：`docs/spike-ssh-forward.md`。

## 1. 示範步驟

```bash
make test-docker                                   # 含遠端主機的完整流程（第一次要建測試主機映像）
XBOT_B11_BENCH=1 XBOT_B11_BOTS=10 mise exec -- go test -tags docker -run Bench ./internal/hosts/  # 量測
```
測試用的「遠端主機」是本機的容器（Docker-in-Docker＋sshd），因為還沒有真的雲端機器。

## 2. 證據

| 項目 | 結果 |
|---|---|
| 斷線（本機 TCP 被切） | 0.07 秒接回，期間 120 個請求全部成功 |
| 遠端 sshd 被殺 | 0.55 秒接回，120 個請求中 10 個失敗 |
| 主機凍結 20 秒 | 約 21 秒偵測到，恢復後立刻接回 |
| 模型請求延遲 | SSH 本身只多 0.1–0.2 毫秒；網路往返 20ms 時約 21ms（重用連線） |
| 10 個 Bot 共用一條連線 | 1000 個同時請求中位數 2.6ms、p95 5ms，全部成功 |
| 主機金鑰被換掉 | 拒絕連線（ErrHostKeyMismatch），不會自動重試 |

## 3. 和規格不一樣的地方

- PRD §16.2 寫 `docker context ssh://`；實作改用同一條 SSH 連線的 `direct-streamlocal`，XBot 主機不需要裝 docker 指令列。
- 雲端主機的 sshd 需要：`AllowTcpForwarding yes`、`AllowStreamLocalForwarding yes`、`StreamLocalBindUnlink yes`、`StreamLocalBindMask 0111`（B12 的安裝指令會設好）。

## 4. 順便找到的問題

- **E02**：同一個 Bot 上一輪剛結束（message.complete）就馬上送下一則，Hermes 會收下但不執行，那一輪卡住。間隔 2 秒以上沒事。已排修正。

## 5. 下一步

B12 主機管理（`make host-add`、`make deploy`、主機清單）、B13（休眠喚醒、唯讀根目錄與網路封鎖）、B14（看畫面與接手）。
