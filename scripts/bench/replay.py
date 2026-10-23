#!/usr/bin/env python3
"""重播使用者真實問過的問題，量回覆的長短、工具、時間、有沒有被叫回去接著做；改了守則前後各跑一次，用 judge.py 比。

2026-10-23 使用者：「回應沒有這麼穠纖合度，現在有點太短，之前有點太長」「好像一直都在瞎忙」，想要有數字。
題目是使用者 10/19～10/21 真的問過的 30 題（閒聊、事實、建議、研究規劃、交代工作、問進度），帶 after 的接在
前一題的對話串裡問。題目和回覆是使用者的私人內容：放在 results/（不進 git）。

用法：XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/replay.py <標籤> [題目 id ...]
  - 一定要用專門測試的 Bot；跑完記得收掉它留在「待我處理」的卡，或直接刪掉測試 Bot。
  - 題目檔：results/replay/questions.jsonl（XBOT_BENCH_REPLAY 可換），一行一題 {"id","q","cat","after"?}。
  - 結果：results/replay/<標籤>/turns.jsonl，一行一題。
"""
import json, os, re, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # python3 -I 不會把這個資料夾放進 sys.path
import run as R  # noqa: E402
QUESTIONS = os.environ.get("XBOT_BENCH_REPLAY") or os.path.join(R.OUT, "replay", "questions.jsonl")

FILLER_HEAD = re.compile(r"^(好的|好喔|沒問題|收到|我先|讓我|我來|以下是|好問題|當然)")
OFFER_END = re.compile(r"(要我|需要我|要不要|需要的話|要的話|你要|想要我)[^。！\n]{0,40}[？?嗎]\s*$")
NARRATION = re.compile(r"(我先|讓我|接下來我|現在去|我去|我會先)")


def shape(text):
    paras = [p for p in re.split(r"\n\s*\n", text.strip()) if p.strip()]
    sentences = [s for s in re.split(r"[。！？!?\n]", text) if s.strip()]
    last = paras[-1] if paras else ""
    return {"chars": len(text), "paras": len(paras), "sentences": len(sentences),
            "bullets": len(re.findall(r"(?m)^\s*([-*•]|\d+[.、])\s", text)), "table": "|---" in text or "| ---" in text,
            "heading": bool(re.search(r"(?m)^#{1,6}\s", text)), "bold": "**" in text,
            "filler_head": bool(FILLER_HEAD.match(text.strip())), "offer_end": bool(OFFER_END.search(last)),
            "narration": bool(NARRATION.search(text))}


def turn(thread, sent_at):
    """這一則之後的回覆、工具、工作與判斷（同一條對話串前面的不算）。"""
    with R.db() as c:
        msgs = c.execute("select sender, content from message where scope='thread' and scope_id=? and kind='text' and created_at>=? order by seq",
                         (thread, sent_at)).fetchall()
        tools = c.execute("select payload from event where thread_id=? and kind='chat.tool_call' and created_at>=?", (thread, sent_at)).fetchall()
        tasks = c.execute("select id, state, verdict, meta from task where thread_id=? and created_at>=? order by created_at", (thread, sent_at)).fetchall()
        usage = c.execute("select coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0) from usage_entry where thread_id=? and created_at>=?",
                          (thread, sent_at)).fetchone()
    names = []
    for (p,) in tools:
        try:
            names.append(json.loads(p)["tool"]["name"])
        except Exception:
            pass
    judged = []
    for _, _, _, meta in tasks:
        try:
            j = (json.loads(meta or "{}").get("judge") or {})
            judged.append(j.get("verdict", ""))
        except Exception:
            judged.append("")
    replies = [m[1] for m in msgs if m[0].startswith("bot:")]
    return {"reply": "\n\n".join(replies), "replies": len(replies), "tools": len(names), "tool_names": names,
            "turns": len(tasks), "judge": judged, "in": usage[0], "out": usage[1]}


def main():
    if not R.BOT or len(sys.argv) < 2:
        sys.exit("用法：XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/replay.py <標籤> [題目 id ...]")
    label, only = sys.argv[1], set(sys.argv[2:])
    items = [json.loads(l) for l in open(QUESTIONS, encoding="utf-8") if l.strip()]
    out_dir = os.path.join(R.OUT, "replay", label)
    os.makedirs(out_dir, exist_ok=True)
    R.wake()
    threads = {}
    done_path = os.path.join(out_dir, "turns.jsonl")
    if os.path.exists(done_path):  # 接著跑：帶 after 的題目接在前一次跑過的那條對話串
        for line in open(done_path, encoding="utf-8"):
            d = json.loads(line)
            threads[d["id"]] = d["thread"]
    with open(os.path.join(out_dir, "turns.jsonl"), "a", encoding="utf-8") as out:
        for it in items:
            if only and it["id"] not in only:
                continue
            args = ["chat", "send", "--bot_id", R.BOT, "--text", it["q"]]
            if it.get("after") and it["after"] in threads:
                args += ["--thread_id", threads[it["after"]]]
            sent_at = int(time.time() * 1000)
            t0 = time.time()
            res = R.x(*args)
            if "error" in res:
                print(it["id"], "送不出去：", res, flush=True)
                continue
            threads[it["id"]] = res["thread_id"]
            state, _ = R.settle(res["thread_id"], res["task_id"], limit_s=900)
            row = {"label": label, "id": it["id"], "cat": it["cat"], "q": it["q"], "state": state, "secs": round(time.time() - t0, 1),
                   "thread": res["thread_id"], **turn(res["thread_id"], sent_at)}
            row.update(shape(row["reply"]))
            out.write(json.dumps(row, ensure_ascii=False) + "\n")
            out.flush()
            print(f'{it["id"]} {it["cat"]:13} {state:12} {row["secs"]:6}s {row["chars"]:5}字 工具{row["tools"]:3} 輪{row["turns"]} {row["judge"]}', flush=True)


if __name__ == "__main__":
    main()
