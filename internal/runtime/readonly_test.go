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
