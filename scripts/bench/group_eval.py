#!/usr/bin/env python3
"""群組合作實測：開一個臨時的三人團隊（主管＋研究＋文案），在群組交代一件要分工、又有一件事要你決定的工作，
量主管怎麼分工、Bot 之間來回幾次、有沒有不用你問就回報、要你決定的事是不是先講，做完把測試用的 Bot、團隊、群組都刪掉。

2026-10-23 使用者：「好像一直都在瞎忙」「我不問你都不講」。對照 10/21 行銷團隊的真實紀錄（14:03～15:06）：
Bot 之間 40 則（請求 19、結果 15、通知 6）、往來最深 7、文案改了 5 輪、4 則只是補傳檔案；第 60 分鐘你問了才回報、要你決定的事在最後一行。

用法：python3 -I scripts/bench/group_eval.py <標籤> [最多等幾分鐘，預設 20]
  - 用你的預設模型開三個新 Bot（測試主管、測試研究、測試文案）；跑完一定刪掉（被打斷也會試著刪）。
    XBOT_BENCH_KEEP=1 時留著查紀錄（id 記在 results/group/kept.json），查完自己刪。
  - 結果：results/group/<標籤>.json，最後印出摘要。
"""
import json, os, sqlite3, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get("XBOT_BENCH_OUT") or os.path.join(HERE, "results")
DB = os.path.expanduser("~/.local/share/xbot/xbot.db")

LEAD, RESEARCH, WRITER = "測試主管", "測試研究", "測試文案"
# 你對決定題的回答（主管用提問卡問你時，過 ANSWER_AFTER 秒照這句回；不回的話主管那一輪會一直等，後面都不會動）。
ANSWER = "帶別人的貨，口吻像朋友推薦，不要硬銷。"
ANSWER_AFTER = 20

KICKOFF = (f"@{LEAD} 這件事請分工：{RESEARCH} 查 3 款台灣現在熱賣的保溫杯（價格、賣點、哪裡買），{WRITER} 依研究結果寫 3 則 Threads 貼文草稿。"
           "要賣自有品牌還是帶別人的貨我還沒決定，會影響貼文口吻，這件事要問我。做完整理在群組給我。")


def x(*args, timeout=300):
    r = subprocess.run(["xbot", *args], capture_output=True, text=True, timeout=timeout)
    try:
        return json.loads(r.stdout)
    except Exception:
        return {"error": {"message": (r.stdout + r.stderr)[-400:]}}


def db():
    return sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=10)


def setup():
    persona = "測試用的 Bot，測完會刪。"
    spec = {"name": "測試團隊", "about": "群組合作實測用，測完會刪", "group_chat": True,
            "lead": {"name": LEAD, "title": "主管", "persona": persona + "你是主管：把工作分給組員，整合後回報使用者。"},
            "members": [{"name": RESEARCH, "title": "研究", "persona": persona + "你負責查資料。"},
                        {"name": WRITER, "title": "文案", "persona": persona + "你負責寫貼文。"}]}
    out = x("teams", "create", "--json", json.dumps(spec, ensure_ascii=False))
    if "error" in out:
        sys.exit("建不了測試團隊：" + json.dumps(out, ensure_ascii=False))
    x("rooms", "update", "--json", json.dumps({"room_id": out["room_id"], "responder_rule": "lead", "lead_id": out["lead_id"]}))
    return out


def teardown(team):
    if not team:
        return
    if os.environ.get("XBOT_BENCH_KEEP") == "1":
        print("留著測試團隊（XBOT_BENCH_KEEP=1），查完要自己刪：", json.dumps({k: team.get(k) for k in ("room_id", "created")}, ensure_ascii=False), flush=True)
        with open(os.path.join(OUT, "group", "kept.json"), "w", encoding="utf-8") as f:
            json.dump({"room_id": team.get("room_id"), "team_id": (team.get("team") or {}).get("id"), "created": team.get("created")}, f)
        return
    if team.get("room_id"):
        x("collab", "stop_room", "--room_id", team["room_id"])
        x("rooms", "delete", "--room_id", team["room_id"])
    if (team.get("team") or {}).get("id"):
        x("teams", "delete", "--id", team["team"]["id"])
    for b in team.get("created") or []:
        x("bots", "delete", "--id", b)


def busy(ids):
    with db() as c:
        q = ",".join("?" * len(ids))
        return c.execute(f"select count(*) from task where bot_id in ({q}) and state not in ('done')", ids).fetchone()[0]


def answer_cards(ids, t0, answered):
    """提問卡：過 ANSWER_AFTER 秒照 ANSWER 回；核准卡：允許這次（測試 Bot 是「先問我」）。回傳第一張提問卡的時間。"""
    q = ",".join("?" * len(ids))
    with db() as c:
        cards = c.execute(f"select id, kind, created_at from inbox_item where bot_id in ({q}) and created_at>=? and status='open' and kind in ('question','approval')",
                          (*ids, t0)).fetchall()
    for cid, kind, at in cards:
        if cid in answered or time.time() * 1000 - at < ANSWER_AFTER * 1000:
            continue
        if kind == "question":
            x("chat", "respond", "--inbox_id", cid, "--text", ANSWER)
        else:
            x("chat", "respond", "--inbox_id", cid, "--choice", "once")
        answered[cid] = kind


def observe(team, t0, limit_s):
    ids = [team["lead_id"], *team["member_ids"]]
    quiet = 0
    answered = {}
    while time.time() - t0 / 1000 < limit_s:
        answer_cards(ids, t0, answered)
        quiet = quiet + 1 if busy(ids) == 0 else 0
        if quiet >= 6:  # 連續 30 秒沒有人在做事、也沒有排隊
            return "settled"
        time.sleep(5)
    return "timeout"


def measure(team, t0, state):
    ids = [team["lead_id"], *team["member_ids"]]
    q = ",".join("?" * len(ids))
    with db() as c:
        posts = c.execute("select created_at, sender, content from message where scope='room' and scope_id=? and created_at>=? and sender like 'bot:%' order by seq",
                          (team["room_id"], t0)).fetchall()
        msgs = c.execute(f"select created_at, from_bot, to_bot, intent, hop, length(text), text from bot_message where from_bot in ({q}) and created_at>=? order by created_at",
                         (*ids, t0)).fetchall()
        usage = c.execute(f"select coalesce(sum(input_tokens),0), coalesce(sum(output_tokens),0), count(*) from usage_entry where bot_id in ({q}) and created_at>=?",
                          (*ids, t0)).fetchone()
        tools = c.execute(f"select count(*) from event where bot_id in ({q}) and kind='chat.tool_call' and created_at>=?", (*ids, t0)).fetchone()[0]
        cards = c.execute(f"select kind, title from inbox_item where bot_id in ({q}) and created_at>=?", (*ids, t0)).fetchall()
    lead = team["lead_id"]
    results = [m for m in msgs if m[3] == "result"]
    requests = [m for m in msgs if m[3] == "request"]
    first_result = {}
    for m in results:
        first_result.setdefault(m[1], m[0])
    revisions = sum(1 for m in requests if m[1] == lead and m[2] in first_result and m[0] > first_result[m[2]])
    lead_posts = [p for p in posts if p[1] == "bot:" + lead]
    last_result = max((m[0] for m in results), default=0)
    sec = lambda ms: round((ms - t0) / 1000) if ms else None
    with db() as c:
        asked_card = c.execute(f"select min(created_at) from inbox_item where bot_id in ({q}) and created_at>=? and kind='question'", (*ids, t0)).fetchone()[0] or 0
    asked_post = next((p[0] for p in lead_posts if "決定" in p[2] and ("自有" in p[2] or "帶別人" in p[2] or "品牌" in p[2])), 0)
    decision_at = min([t for t in (asked_card, asked_post) if t], default=0)
    return {
        "state": state,
        "room_posts": len(posts), "lead_posts": len(lead_posts),
        "bot_messages": len(msgs), "by_intent": {k: sum(1 for m in msgs if m[3] == k) for k in ("request", "question", "result", "fyi")},
        "max_hop": max((m[4] for m in msgs), default=0), "peer_chars": sum(m[5] for m in msgs),
        "results": len(results), "revisions": revisions,
        "first_delegation_s": sec(min((m[0] for m in requests if m[1] == lead), default=0)),
        "decision_asked_s": sec(decision_at),
        "decision_before_results_done": bool(decision_at) and (not last_result or decision_at <= last_result),
        "final_report_s": sec(lead_posts[-1][0]) if lead_posts else None,
        "reported_after_last_result": bool(lead_posts) and lead_posts[-1][0] >= last_result,
        "tokens_in": usage[0], "tokens_out": usage[1], "model_calls": usage[2], "tool_calls": tools,
        "cards": [f"{k}：{t}" for k, t in cards],
        "posts": [{"s": sec(p[0]), "by": p[1][-6:], "text": p[2][:300]} for p in posts],
    }


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    label = sys.argv[1]
    limit = int(sys.argv[2]) * 60 if len(sys.argv) > 2 else 20 * 60
    team = None
    os.makedirs(os.path.join(OUT, "group"), exist_ok=True)
    try:
        team = setup()
        t0 = int(time.time() * 1000)
        post = x("rooms", "post", "--json", json.dumps({"room_id": team["room_id"], "text": KICKOFF}, ensure_ascii=False))
        if "error" in post:
            sys.exit("發不出去：" + json.dumps(post, ensure_ascii=False))
        state = observe(team, t0, limit)
        m = measure(team, t0, state)
    finally:
        teardown(team)
    with open(os.path.join(OUT, "group", label + ".json"), "w", encoding="utf-8") as f:
        json.dump(m, f, ensure_ascii=False, indent=1)
    show = {k: v for k, v in m.items() if k not in ("posts",)}
    print(json.dumps(show, ensure_ascii=False, indent=1))
    for p in m["posts"]:
        print(f'{p["s"]:>5}s {p["by"]}：{p["text"][:160]}')


if __name__ == "__main__":
    main()
