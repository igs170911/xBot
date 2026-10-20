package runtime

import (
	"encoding/json"
	"testing"

	"xbot/internal/engine"
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
