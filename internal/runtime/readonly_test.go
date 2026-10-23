package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

func TestReadOnlyScript(t *testing.T) {
	ok := []string{
		// 2026-10-20 基準測試裡 Bot 真的跑的：讀筆記資料夾
		"from hermes_tools import read_file, terminal\nimport os\np=\"/workspace/bench/notes\"\nfor f in sorted(os.listdir(p)):\n    fp=os.path.join(p,f)\n    st=os.stat(fp)\n    print(\"=\"*20, f, st.st_size, \"bytes\")\n    print(read_file(fp)[\"content\"][:3000])\n",
		"import csv, collections\nc=collections.Counter()\nwith open('/workspace/bench/sales.csv', newline='', encoding='utf-8') as f:\n    for r in csv.DictReader(f): c[r['品類']]+=int(r['單價'])*int(r['數量'])\nprint(c.most_common())",
		"import json\nd=json.load(open('/workspace/a.json','r'))\nprint(len(d))",
		"from pathlib import Path\nfor p in Path('/workspace').glob('*.md'): print(p.name, p.read_text()[:200].replace('\\n',' '))",
		"from hermes_tools import web_search\nfor q in ['蝦皮 聯盟', 'momo 分潤']:\n    print(web_search(q))",
	}
	for _, c := range ok {
		if !ReadOnlyScript(c) {
			t.Errorf("應該算只讀：\n%s", c)
		}
	}
	bad := []string{
		"open('/workspace/out.md','w').write('x')",
		"with open('/workspace/x', mode='a') as f: f.write('y')",
		"m='w'\nopen('/workspace/x', m)",
		"import os\nos.remove('/workspace/a')",
		"import shutil\nshutil.rmtree('/workspace')",
		"import subprocess\nsubprocess.run(['ls'])",
		"from hermes_tools import terminal\nterminal('rm -rf /workspace/bench')",
		"from hermes_tools import write_file\nwrite_file('/workspace/a', 'x')",
		"import pandas as pd\npd.read_csv('a.csv').to_csv('b.csv')",
		"from pathlib import Path\nPath('/workspace/a').write_text('x')",
		"import requests\nrequests.post('https://x', data=open('/workspace/a').read())",
		"exec(\"import os; os.system('id')\")",
		"getattr(__import__('o'+'s'), 'sys'+'tem')('id')",
		"import os as o\no.system('id')",
		"from os import system\nsystem('id')",
		"import sqlite3\nsqlite3.connect('/workspace/x.db').execute('drop table t')",
		"open('/workspace/x'",
		"",
	}
	for _, c := range bad {
		if ReadOnlyScript(c) {
			t.Errorf("不能算只讀：\n%s", c)
		}
	}
}

func TestReadOnlyExecuteCodeRequest(t *testing.T) {
	req := func(command, key string) *engine.Request {
		p, _ := json.Marshal(map[string]any{"command": command, "pattern_key": key, "pattern_keys": []string{key}})
		return &engine.Request{ID: "r1", Kind: "approval", Params: p}
	}
	if !readOnlyExecuteCode(req("execute_code <<'PY'\nprint(open('/workspace/a').read())\nPY", "execute_code")) {
		t.Fatal("只讀的 execute_code 要自動允許")
	}
	if readOnlyExecuteCode(req("execute_code <<'PY'\nopen('/workspace/a','w')\nPY", "execute_code")) {
		t.Fatal("會寫檔的要問")
	}
	if readOnlyExecuteCode(req("rm -rf /workspace", "recursive_delete")) {
		t.Fatal("危險的終端機指令一律要問")
	}
	if readOnlyExecuteCode(&engine.Request{ID: "r2", Kind: "clarify"}) {
		t.Fatal("提問不是核准")
	}
}

// Hermes 要整段核准一段只讀的 execute_code：XBot 自己允許這一次，不開卡；會寫檔的照舊開卡等你。
func TestReadOnlyScriptAutoAllowed(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	var answer json.RawMessage
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		code := "import csv\nprint(sum(1 for _ in csv.reader(open('/workspace/a.csv'))))"
		answer = tr.Request("approval", map[string]any{"command": "execute_code <<'PY'\n" + code + "\nPY", "pattern_key": "execute_code",
			"pattern_keys": []string{"execute_code"}, "choices": []string{"once", "session", "always", "deny"}})
		tr.Complete("一共 12 行", "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "a.csv 有幾行？")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	if !strings.Contains(string(answer), "once") {
		t.Fatalf("只讀的程式要自動允許這一次：%s", answer)
	}
	if items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{TaskID: res.TaskID, Kinds: []string{"approval"}}); len(items) != 0 {
		t.Fatalf("不該開核准卡：%+v", items)
	}

	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		go tr.Request("approval", map[string]any{"command": "execute_code <<'PY'\nopen('/workspace/a.csv','w').write('')\nPY", "pattern_key": "execute_code",
			"pattern_keys": []string{"execute_code"}, "choices": []string{"once", "deny"}})
		time.Sleep(300 * time.Millisecond)
		tr.Complete("好", "complete", nil)
	}
	res, err = f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "把 a.csv 清空")
	if err != nil {
		t.Fatal(err)
	}
	if card := f.waitCard(t, res.TaskID); card.Kind != "approval" {
		t.Fatalf("會寫檔的要開核准卡：%+v", card)
	}
}

// B.4: 高風險瀏覽器操作（表單提交、購買、登入、發送等）需要人工核准（伺服器端攔截）。
func TestConsequentialBrowserActionRequiresApproval(t *testing.T) {
	req := func(command, key string, keys []string) *engine.Request {
		if keys == nil {
			keys = []string{key}
		}
		p, _ := json.Marshal(map[string]any{"command": command, "pattern_key": key, "pattern_keys": keys})
		return &engine.Request{ID: "r1", Kind: "approval", Params: p}
	}

	// 高風險操作：需要核准
	consequential := []struct {
		desc    string
		command string
		key     string
		keys    []string
	}{
		{"表單提交", "browser_exec('click', selector='button[type=submit]')", "browser_exec", nil},
		{"輸入密碼", "cdp('Input.dispatchKeyEvent', text='password123')", "cdp_", nil},
		{"購買操作", "browser_click('button.buy-now')", "browser_", []string{"browser_click"}},
		{"發送訊息", "browser_exec('send_message', text='hello')", "browser_exec", nil},
		{"登入操作", "browser_signin('user@example.com')", "browser_", []string{"browser_signin"}},
		{"刪除操作", "browser_exec('delete_item')", "browser_exec", nil},
		{"確認購買", "browser_click('button#confirm-purchase')", "browser_", []string{"browser_click"}},
	}

	for _, tc := range consequential {
		if !consequentialBrowserAction(req(tc.command, tc.key, tc.keys)) {
			t.Errorf("%s 應該需要核准：%s", tc.desc, tc.command)
		}
	}

	// 低風險操作：可以不攔截（但目前所有 browser 操作都會走 Hermes 的 DANGEROUS_PATTERNS，這裡只驗證邏輯）
	safe := []struct {
		desc    string
		command string
		key     string
	}{
		{"讀取內容", "read_file('/workspace/a.txt')", "read_file"},
		{"執行查詢", "execute_code <<'PY'\nprint(1+1)\nPY", "execute_code"},
		{"非瀏覽器操作", "terminal('ls')", "terminal"},
	}

	for _, tc := range safe {
		if consequentialBrowserAction(req(tc.command, tc.key, nil)) {
			t.Errorf("%s 不應該被誤判為瀏覽器操作：%s", tc.desc, tc.command)
		}
	}
}

// 驗證高風險瀏覽器操作會開核准卡，不會自動允許（bypass test）。
func TestBrowserActionApprovalBypass(t *testing.T) {
	ctx := context.Background()
	f := setup(t)

	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		// 模擬 Hermes 請求核准瀏覽器提交操作
		go tr.Request("approval", map[string]any{
			"command":      "browser_exec('click', selector='button[type=submit]')",
			"pattern_key":  "browser_exec",
			"pattern_keys": []string{"browser_exec"},
			"choices":      []string{"once", "deny"},
		})
		time.Sleep(300 * time.Millisecond)
		tr.Complete("等待核准", "complete", nil)
	}

	res, err := f.rt.Chat(ctx, f.bot.ID, "", "提交表單")
	if err != nil {
		t.Fatal(err)
	}

	// 驗證：應該開核准卡
	card := f.waitCard(t, res.TaskID)
	if card.Kind != "approval" {
		t.Fatalf("瀏覽器高風險操作應該開核准卡：%+v", card)
	}

	// 驗證：工作狀態應該是 waiting_approval
	task, _ := f.db.GetTask(ctx, res.TaskID)
	if task.State != store.TaskWaitingApproval {
		t.Fatalf("工作應該在等核准：%s", task.State)
	}
}
