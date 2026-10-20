#!/usr/bin/env python3
"""比對兩種設定跑同一題的行為（harness2 的 behavioral contrast）：哪一題一邊做完、一邊沒有，工具怎麼用、花多少、最後怎麼收尾。
用來把「聰明模型做得到、笨模型做不到」的差別寫成給笨模型看的守則（2026-10-20：「網站被擋就換比價網站」就是這樣來的）。

用法：python3 -I scripts/bench/contrast.py <好的設定> <要改善的設定> [results.jsonl]
例：python3 -I scripts/bench/contrast.py hermes-luna hermes-deepseek
"""
import collections, json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))


def load(path):
    last = {}
    for line in open(path):
        r = json.loads(line)
        last[(r["config"], r["task"])] = r
    return last


def ok(r):
    # 一次做完、該存的檔案有存
    return r["state"] == "done" and len(r.get("verdicts", [])) == 1 and r.get("file_bytes") != 0


def reply_end(out_dir, cfg, task, n=160):
    p = os.path.join(out_dir, "out", cfg, task + ".md")
    if not os.path.exists(p):
        return ""
    text = open(p).read().split("\n\n", 2)[-1].strip().replace("\n", " ")
    return ("…" + text[-n:]) if len(text) > n else text


def steps(names):
    c = collections.Counter(names)
    return ", ".join(f"{k}×{v}" for k, v in c.most_common())


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    good, weak = sys.argv[1], sys.argv[2]
    path = sys.argv[3] if len(sys.argv) > 3 else os.path.join(os.environ.get("XBOT_BENCH_OUT") or os.path.join(HERE, "results"), "results.jsonl")
    out_dir = os.path.dirname(path)
    last = load(path)
    tasks = sorted({t for (c, t) in last if c in (good, weak)})
    print(f"比對 {good}（好的）↔ {weak}（要改善的），共 {len(tasks)} 題\n")
    for t in tasks:
        g, w = last.get((good, t)), last.get((weak, t))
        if not g or not w:
            continue
        flag = "  "
        if ok(g) and not ok(w):
            flag = "❗"  # 好的做完、要改善的沒有：最值得寫成守則
        elif w["in"] > 2 * max(g["in"], 1) or w["secs"] > 2 * max(g["secs"], 1):
            flag = "💸"  # 都做完，但花了兩倍以上
        print(f"{flag} {t}")
        for name, r in ((good, g), (weak, w)):
            print(f"     {name:16s} {'做完' if ok(r) else '沒做完'} {r['secs']:4d}s 輸入 {r['in']:>8,} 工具 {len(r.get('tool_names', []))} 輪 {r['turns']}")
            print(f"       步驟：{steps(r.get('tool_names', [])) or '（沒用工具）'}")
            print(f"       收尾：{reply_end(out_dir, name, t)}")
        print()
    print("❗ 一邊做完、一邊沒有　💸 都做完但花兩倍以上。看兩邊步驟差在哪，寫成守則或技能，再用 run.py 重跑那一題確認。")


if __name__ == "__main__":
    main()
