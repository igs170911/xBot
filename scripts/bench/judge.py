#!/usr/bin/env python3
"""兩兩比較同一題的兩個回覆（例：舊守則 vs 新守則），請比較強的模型評哪個講得恰到好處（2026-10-23 使用者：「穠纖合度」）。

用法：XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/judge.py <A 標籤> <B 標籤> [模型]
      XBOT_BENCH_BOT=<測試用 Bot 的 id> python3 -I scripts/bench/judge.py --calibrate [模型]
  - A、B 是 replay.py 的標籤（results/replay/<標籤>/turns.jsonl）。
  - 模型預設 kimi-k3（OpenCode Go；claude-haiku 只支援 Anthropic 格式，這裡用不了）。經測試 Bot 電腦裡的 hermes_host 轉送到 Model Proxy：真的金鑰只在 Model Proxy，
    Bot 的 worker-key 不離開它的電腦、不印出來；用量記在測試 Bot 名下。
  - 每一題 A、B 換位置各問一次，兩次都選同一邊才算贏，其他算平手（避免模型偏好第一個）。
  - --calibrate：先拿使用者真的反應過的 10 則回覆（results/replay/calib.jsonl）試評審，對 8 則以上才信它的分數。
  - 結果：results/replay/judge-<A>-<B>.jsonl，最後印出每類的摘要。
"""
import json, os, subprocess, sys, time
from collections import Counter, defaultdict
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # python3 -I 不會把這個資料夾放進 sys.path
import run as R  # noqa: E402

REPLAY = os.path.join(R.OUT, "replay")

# 使用者說過、確認過的回話偏好（memory reply-style-confirmed、2026-10-20～23 的回饋）。
PREFS = ("使用者對回覆的偏好：像懂行的朋友在通訊軟體回訊息，自然口語、句子接得順，不要電報式的一句一段，也不要公文或長報告。"
         "簡單問題三到五句、兩段內，第一句就回答。要研究、規劃的事要真的查過，給結論和骨架（誰做什麼、順序、關鍵數字），細節放檔案。"
         "交代的工作要在這一輪做完，不要一步一步問「要我…嗎」；要他決定的事放在看得到的地方，不要埋在最後。"
         "不寫開場白、不重述問題、不描述過程、不客套收尾；不需要的格式（標題、粗體、長清單）不要。")

# 判斷長短的錨點（照使用者的反應：「禿頭怎麼辦，落健有用嗎」88 字兩段太短太碎、178 字剛好、424 和 484 字太長）。
ANCHORS = ("長短的標準：閒聊一兩句；簡單問題、問事實、問建議大約三到五句、兩段內（一百多字），超過五句、超過兩段或超過 300 字算 too_long；"
           "只有一兩句、或每段只有一句、讀起來像電報算 choppy，資訊不夠讓他判斷或下一步要追問的算 too_short；"
           "研究、規劃給結論和骨架、一個手機畫面看得完算剛好，長篇細節沒放檔案算 too_long。"
           "結尾問「要我…嗎」「要繼續…嗎」「要不要…」這種要使用者推下一步才會做的，就算 closing_offer／offer_instead。")

RUBRIC = (ANCHORS + "請評兩個回覆（A、B）。每個回覆給：length（too_short｜just_right｜too_long）、"
          "choppy（每段只有一句、讀起來像電報）、answers_first（第一句就回應重點）、missing（使用者接下來還會需要、卻沒給的，最多三項）、"
          "filler（有的才列：opening 開場白、restates 重述問題、process_narration 描述過程、closing_offer 結尾問要不要再做、"
          "needless_question 不必要的提問、format_heavy 格式太重）、complete（0 沒回答到、1 部分、2 完整）。"
          "最後 better：A、B 或 tie（差不多時選 tie），why：一句理由（40 字內）。只回 JSON："
          '{"A":{"length":"","choppy":false,"answers_first":true,"missing":[],"filler":[],"complete":2},"B":{...},"better":"A","why":""}')

CALIB_RUBRIC = (ANCHORS + "請評這個回覆。給 length（too_short｜just_right｜too_long）、choppy、offer_instead（交代的事沒做完，結尾問要不要做）、"
                "only_promised（只說要做或已開始，實際內容沒給）。只回 JSON："
                '{"length":"","choppy":false,"offer_instead":false,"only_promised":false}')

# 在測試 Bot 的電腦裡呼叫模型：讀它自己的 worker-key，經 127.0.0.1:8642 的 hermes_host 送到 Model Proxy。
CALL = r'''
import json, sys, urllib.request, urllib.error
req = json.loads(sys.stdin.read())
key = open("/hermes/.worker-key").read().strip()
r = urllib.request.Request("http://127.0.0.1:8642/model/openai/v1/chat/completions", data=json.dumps(req).encode(),
    headers={"Authorization": "Bearer " + key, "content-type": "application/json"})
try:
    print(json.loads(urllib.request.urlopen(r, timeout=180).read())["choices"][0]["message"]["content"])
except urllib.error.HTTPError as e:
    print("HTTP %d %s" % (e.code, e.read()[:200].decode("utf-8", "replace")))
except Exception as e:
    print("ERR %s" % e)
'''


def ask(model, system, user, tries=3):
    """問一次評審；出錯（限流、逾時）等一下再試，最多 tries 次。"""
    body = {"model": model, "temperature": 0, "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}]}
    err = ""
    for i in range(tries):
        try:
            r = subprocess.run(["docker", "exec", "-i", "-u", "1000", R.BOX, "/opt/hermes/.venv/bin/python", "-I", "-c", CALL],
                               input=json.dumps(body), capture_output=True, text=True, timeout=240)
            out = r.stdout.strip()
        except subprocess.TimeoutExpired:
            out = "ERR timeout"
        start, end = out.find("{"), out.rfind("}")
        if start >= 0 and not out.startswith(("HTTP ", "ERR ")):
            try:
                return json.loads(out[start:end + 1])
            except Exception:
                pass
        err = (out or "")[-200:]
        time.sleep(10 * (i + 1))
    return {"error": err}


def load(label):
    path = os.path.join(REPLAY, label, "turns.jsonl")
    rows = {}
    for line in open(path, encoding="utf-8"):
        d = json.loads(line)
        rows[d["id"]] = d  # 同一題跑了兩次時用最後一次
    return rows


def calibrate(model):
    labels = [json.loads(l) for l in open(os.path.join(REPLAY, "calib.jsonl"), encoding="utf-8")]
    ok = 0
    for c in labels:
        got = ask(model, "你是嚴格的對話品質評審。" + PREFS, json.dumps({"使用者的訊息": c["q"], "回覆": c["reply"], "評分方式": CALIB_RUBRIC}, ensure_ascii=False))
        want = c["label"].rstrip("?")
        hit = {"too_long": got.get("length") == "too_long", "just_right": got.get("length") == "just_right",
               "too_short_choppy": got.get("length") == "too_short" or got.get("choppy") is True,
               "handed_back_offer": got.get("offer_instead") is True, "only_promised": got.get("only_promised") is True}.get(want, False)
        ok += hit
        print(("✓" if hit else "✗"), want, "←", json.dumps(got, ensure_ascii=False)[:160], flush=True)
    print(f"評審對了 {ok}/{len(labels)}（8 以上才信它的分數）", flush=True)


def judge_pair(model, a_label, b_label, ra, rb):
    user = {"題目類型": ra["cat"], "使用者的訊息": ra["q"], "評分方式": RUBRIC}
    first = ask(model, "你是嚴格的對話品質評審。" + PREFS, json.dumps({**user, "回覆A": ra["reply"], "回覆B": rb["reply"]}, ensure_ascii=False))
    second = ask(model, "你是嚴格的對話品質評審。" + PREFS, json.dumps({**user, "回覆A": rb["reply"], "回覆B": ra["reply"]}, ensure_ascii=False))
    if "error" in first or "error" in second:
        return None, (first.get("error") or second.get("error"))
    swap = {"A": "B", "B": "A", "tie": "tie"}
    v1, v2 = first.get("better"), swap.get(second.get("better"), "?")
    better = v1 if v1 == v2 and v1 in ("A", "B") else "tie"
    return {"id": ra["id"], "cat": ra["cat"], "q": ra["q"], "better": better, "why": first.get("why", ""),
            a_label: {**(first.get("A") or {}), "chars": ra["chars"], "tools": ra["tools"], "secs": ra["secs"], "turns": ra["turns"]},
            b_label: {**(first.get("B") or {}), "chars": rb["chars"], "tools": rb["tools"], "secs": rb["secs"], "turns": rb["turns"]}}, None


def compare(a_label, b_label, model):
    """接著跑：已經評過的題目不再評；評審出錯的不記，下次再試。"""
    a, b = load(a_label), load(b_label)
    out_path = os.path.join(REPLAY, f"judge-{a_label}-{b_label}.jsonl")
    done = {}
    if os.path.exists(out_path):
        for line in open(out_path, encoding="utf-8"):
            d = json.loads(line)
            done[d["id"]] = d
    todo = [q for q in sorted(set(a) & set(b)) if q not in done and a[q].get("reply") and b[q].get("reply")]
    with open(out_path, "a", encoding="utf-8") as out, ThreadPoolExecutor(2) as pool:
        for row, err in pool.map(lambda q: judge_pair(model, a_label, b_label, a[q], b[q]), todo):
            if row is None:
                print("評審出錯，下次再試：", str(err)[:160], flush=True)
                continue
            out.write(json.dumps(row, ensure_ascii=False) + "\n")
            out.flush()
            done[row["id"]] = row
            print(f'{row["id"]} {row["cat"]:13} 勝：{ {"A": a_label, "B": b_label}.get(row["better"], "平手") }  {row["why"]}', flush=True)
    summary([done[q] for q in sorted(done)], a_label, b_label)


def summary(results, a_label, b_label):
    by = defaultdict(list)
    for r in results:
        by[r["cat"]].append(r)
        by["全部"].append(r)
    print(f"\n| 類別 | 題數 | {a_label} 勝 | {b_label} 勝 | 平手 | 剛好（{a_label}→{b_label}） | 太短 | 太長 | 一句一段 | 結尾問要不要 | 字數中位數 |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    med = lambda xs: sorted(xs)[len(xs) // 2] if xs else 0
    for cat, rows in by.items():
        c = Counter(r["better"] for r in rows)
        def rate(label, key, val):
            return sum(1 for r in rows if (r[label].get(key) == val if not isinstance(val, str) or key != "filler" else val in r[label].get("filler", [])))
        cell = lambda key, val: f"{rate(a_label, key, val)}→{rate(b_label, key, val)}"
        print(f'| {cat} | {len(rows)} | {c["A"]} | {c["B"]} | {c["tie"]} | {cell("length", "just_right")} | {cell("length", "too_short")} | '
              f'{cell("length", "too_long")} | {cell("choppy", True)} | {cell("filler", "closing_offer")} | '
              f'{med([r[a_label]["chars"] for r in rows])}→{med([r[b_label]["chars"] for r in rows])} |')


def main():
    if not R.BOT:
        sys.exit("要設 XBOT_BENCH_BOT（測試用 Bot 的 id；模型經它的電腦呼叫）")
    R.wake()  # 測試 Bot 的電腦閒置會休眠；評審經它的電腦呼叫模型
    if len(sys.argv) >= 2 and sys.argv[1] == "--calibrate":
        return calibrate(sys.argv[2] if len(sys.argv) > 2 else "kimi-k3")
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    compare(sys.argv[1], sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "kimi-k3")


if __name__ == "__main__":
    main()
