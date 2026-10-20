#!/usr/bin/env python3
"""XBot 基準測試：同一組照使用者真實用法出的任務，在不同引擎／模型設定下跑，量「有沒有一次做完、token、工具次數、時間」。

借 harness2 的做法（同一組任務、改一個地方、比前後的行為），用在 XBot 自己的守則、技能、快判與 Hermes 設定上：
改了這些就重跑，看每一題的結果。2026-10-20 用它找出：看不懂圖片、grok-build 技能卡住、快判把打招呼當成沒做完、子代理讓研究多花 4 倍 token。

用法：XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/run.py <設定名> [任務 id ...]
  - 一定要用專門測試的 Bot（會換它的引擎與模型、清掉它的 /workspace/bench）；不要用正在用的 Bot。
  - 設定：見 CONFIGS（Hermes＋各供應商、訂閱 CLI）。用訂閱 CLI 的設定要那個 Bot 已經登入或同意用這台電腦的登入。
  - 結果一行一個 JSON 附加到 results.jsonl（XBOT_BENCH_OUT 可換資料夾），回覆全文存在 out/<設定>/<任務>.md。
  - 跑完記得收掉測試 Bot 留在「待我處理」的卡（接手、核准）。
  - XBOT_BENCH_APPROVE=once：測試 Bot 停下來等核准時替它按「允許這次」接著跑（只對測試 Bot），結果記下按了幾次
    （approvals：用這個權限等級，使用者要被問幾次）。沒設時遇到核准就停（needs_person）。
"""
import json, os, sqlite3, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get("XBOT_BENCH_OUT") or os.path.join(HERE, "results")
BOT = os.environ.get("XBOT_BENCH_BOT", "")
DB = os.path.expanduser("~/.local/share/xbot/xbot.db")
BOX = "xbot-box-" + BOT

CONFIGS = {
    "hermes-deepseek": ("hermes", "openai/deepseek-v4.1-flash", False),
    "hermes-grok47": ("hermes", "openai/grok-4.7", False),
    "hermes-gpt6sol": ("hermes", "openai-codex/gpt-6-sol", False),
    "hermes-luna": ("hermes", "openai-codex/gpt-5.6-luna", False),
    "hermes-haiku": ("hermes", "openai/claude-haiku-5-5", False),
    "hermes-beam": ("hermes", "openai/beam-free", False),
    "grok-cli": ("grok", "", False),
    "codex-cli": ("codex", "", False),
    "claude-code": ("claude-code", "", True),
    "claude-sonnet": ("claude-code", "sonnet", True),
    "opencode-cli": ("opencode", "", True),
}

TASKS = [
    ("t01-hi", "嗨，你今天可以幫我做什麼？"),
    ("t02-files", "看一下 /workspace/bench/notes 裡有哪些檔案，哪一份最值得我先看？為什麼？"),
    ("t03-data", "/workspace/bench/sales.csv 是上個月的銷售紀錄。算出每個品類的營收，找出營收最高的三個商品，整理成表格給我，也存成 /workspace/bench/sales-report.md"),
    ("t04-write", "幫我寫一篇 IG 貼文，介紹特斯拉 Model Y 的中控置物盒，250 字左右，口吻輕鬆，最後附 5 個 hashtag"),
    ("t05-search", "蝦皮聯盟行銷和 momo 聯盟行銷的分潤比例跟申請門檻差在哪？給我結論"),
    ("t06-todo", "把 /workspace/bench/notes 裡所有還沒做完的待辦整理成一份清單，照截止日排序，標出誰負責，存成 /workspace/bench/todo.md"),
    ("t07-web", "做一個簡單的網頁 /workspace/bench/site/index.html，比較豆腐砂、礦砂、松木砂三種貓砂（價格帶、優點、缺點），要有表格。做好後用瀏覽器打開，確認表格有正確顯示再告訴我"),
    ("t08-diagram", "用流程圖告訴我：我在 XBot 傳一則訊息給你之後，到你回覆我之間發生了什麼"),
    ("t09-research", "使用技能「deep-research」：台灣現在最熱賣的三種貓砂類型和代表品牌，各自的價格帶"),
    ("t10-shop", "幫我找三款適合特斯拉 Model 3 的車用垃圾桶（要在蝦皮買得到），比較價格和評價，做成表格存成 /workspace/bench/trash-bins.md，最後給我推薦"),
]

FILES = {"t03-data": "/workspace/bench/sales-report.md", "t06-todo": "/workspace/bench/todo.md",
         "t07-web": "/workspace/bench/site/index.html", "t10-shop": "/workspace/bench/trash-bins.md"}


def x(*args, inp=None, timeout=400):
    r = subprocess.run(["xbot", *args], input=inp, capture_output=True, text=True, timeout=timeout)
    try:
        return json.loads(r.stdout)
    except Exception:
        return {"error": {"message": (r.stdout + r.stderr)[-500:]}}


def db():
    return sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=10)


def seed():
    # 每個設定開始前把 /workspace/bench 換回原本的樣子（上一個設定做的成品不能留著）。
    subprocess.run(["docker", "exec", "-u", "1000", BOX, "rm", "-rf", "/workspace/bench"], check=True)
    subprocess.run(["docker", "cp", os.path.join(HERE, "seed") + "/.", BOX + ":/workspace/bench"], check=True)


def wake():
    # 開機（第一次 send 也會開，但先開好比較不會算到第一題的時間裡）
    subprocess.run(["xbot", "computers", "wake", "--bot_id", BOT], capture_output=True, timeout=300)
    for _ in range(60):
        if subprocess.run(["docker", "exec", BOX, "true"], capture_output=True).returncode == 0:
            return
        time.sleep(2)


def thread_tasks(thread):
    with db() as c:
        return c.execute("select id, state, verdict, source, created_at, finished_at from task where thread_id=? order by created_at", (thread,)).fetchall()


APPROVE = os.environ.get("XBOT_BENCH_APPROVE", "")


def settle(thread, first_task, limit_s=1500):
    """等這條對話串做完：自動繼續會開新的工作，要等到沒有新的工作為止。回傳（狀態, 替它核准了幾次）。"""
    start = time.time()
    approvals = 0
    x("chat", "wait_until_settled", "--task_id", first_task, "--timeout_s", "300")
    quiet = 0
    while time.time() - start < limit_s:
        ts = thread_tasks(thread)
        open_ = [t for t in ts if t[1] != "done"]
        if open_:
            if open_[-1][1] == "waiting_approval" and APPROVE == "once":
                w = x("chat", "wait_until_settled", "--task_id", open_[-1][0], "--timeout_s", "1")
                card = (w.get("request") or {}).get("inbox_id")
                if card and "error" not in x("chat", "respond", "--inbox_id", card, "--choice", "once"):
                    approvals += 1
                    continue
            if open_[-1][1] in ("waiting_input", "waiting_approval"):
                return "needs_person", approvals
            x("chat", "wait_until_settled", "--task_id", open_[-1][0], "--timeout_s", "300")
            quiet = 0
            continue
        quiet += 1
        if quiet >= 4:  # 連續 8 秒沒有新工作
            return "done", approvals
        time.sleep(2)
    x("chat", "stop", "--bot_id", BOT)
    return "timeout", approvals


def measure(thread):
    with db() as c:
        u = c.execute("select count(*), coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), coalesce(sum(cache_read_tokens),0), coalesce(sum(cost_micros),0) from usage_entry where thread_id=?", (thread,)).fetchone()
        tools = c.execute("select payload from event where thread_id=? and kind='chat.tool_call'", (thread,)).fetchall()
        usage_ev = c.execute("select payload from event where thread_id=? and kind='chat.usage'", (thread,)).fetchall()
        msgs = c.execute("select sender, content from message where scope='thread' and scope_id=? and kind='text' order by seq", (thread,)).fetchall()
    names = []
    for (p,) in tools:
        try:
            names.append(json.loads(p)["tool"]["name"])
        except Exception:
            pass
    ev_in = ev_out = ev_cache = 0
    for (p,) in usage_ev:
        try:
            us = json.loads(p)["usage"]
            ev_in += us.get("input_tokens", 0) or 0
            ev_out += us.get("output_tokens", 0) or 0
            ev_cache += us.get("cache_read_tokens", 0) or 0
        except Exception:
            pass
    replies = [m[1] for m in msgs if m[0] not in ("owner", "user", "")]
    return {"calls": u[0], "in": u[1] or ev_in, "out": u[2] or ev_out, "cache": u[3] or ev_cache, "cost_usd": u[4] / 1e6,
            "tools": len(names), "tool_names": names, "reply": "\n\n---（下一則）---\n\n".join(replies), "replies": len(replies)}


def file_ok(task):
    p = FILES.get(task)
    if not p:
        return None
    r = subprocess.run(["docker", "exec", BOX, "sh", "-c", f"test -s '{p}' && wc -c < '{p}'"], capture_output=True, text=True)
    return int(r.stdout.strip()) if r.returncode == 0 else 0


def main():
    if not BOT or len(sys.argv) < 2 or sys.argv[1] not in CONFIGS:
        sys.exit("用法：XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/run.py <" + "|".join(CONFIGS) + "> [任務 id ...]")
    cfg = sys.argv[1]
    only = set(sys.argv[2:])
    engine, model, link = CONFIGS[cfg]
    sw = x("engines", "switch", "--json", json.dumps({"bot_id": BOT, "engine": engine, "model": model, "link": link}))
    if "error" in sw:
        print("換不過去：", sw, flush=True)
        sys.exit(1)
    wake()
    seed()
    os.makedirs(os.path.join(OUT, "out", cfg), exist_ok=True)  # 也建好 OUT
    for tid, text in TASKS:
        if only and tid not in only:
            continue
        t0 = time.time()
        s = x("chat", "send", "--bot_id", BOT, "--text", text)
        if "error" in s:
            print(tid, "送不出去", s, flush=True)
            continue
        state, approvals = settle(s["thread_id"], s["task_id"])
        ts = thread_tasks(s["thread_id"])
        m = measure(s["thread_id"])
        rec = {"config": cfg, "task": tid, "state": state, "approvals": approvals, "secs": round(time.time() - t0), "turns": len(ts),
               "verdicts": [t[2] for t in ts], "file_bytes": file_ok(tid), "thread": s["thread_id"], **{k: v for k, v in m.items() if k != "reply"}}
        with open(os.path.join(OUT, "results.jsonl"), "a") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        with open(os.path.join(OUT, "out", cfg, tid + ".md"), "w") as f:
            f.write(f"# {tid}\n\n> {text}\n\n" + m["reply"])
        print(f"{cfg} {tid}: {state} approvals={approvals} turns={len(ts)} {rec['secs']}s in={m['in']} out={m['out']} tools={m['tools']} file={rec['file_bytes']} verdicts={rec['verdicts']}", flush=True)


if __name__ == "__main__":
    main()
