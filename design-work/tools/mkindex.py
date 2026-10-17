#!/usr/bin/env python3
"""Build project/X01-Index.dc.html (feature ↔ artboard ↔ PRD ↔ status) from specs/*.json."""
import json, glob, os, sys
sys.path.insert(0, os.path.dirname(__file__))
ROOT = '/tmp/claude-1000/-home-daniel-Projects/119fe167-bf85-4b35-9f2a-d88d25ba20e9/scratchpad/xbot-design'
src = open(os.path.join(os.path.dirname(__file__), 'assemble.py'), encoding='utf-8').read()
rows_src = src[src.index('ROWS = ['):src.index(']\nNOTE_W')] + ']'
ns = {}; exec(rows_src, ns); ROWS = ns['ROWS']
specs = {}
for f in glob.glob(ROOT + '/specs/*.json'):
    try: d = json.load(open(f, encoding='utf-8'))
    except Exception: continue
    n = d.get('file') or os.path.basename(f)[:-5] + '.dc.html'
    if not n.endswith('.dc.html'): n += '.dc.html'
    specs[n] = d
groups = []
total = 0; cnt = {'已畫': 0, '簡化過': 0, '待定': 0}
for key, title, files in ROWS:
    if key == 'X': continue
    items = []
    for f in files:
        f = f if f.endswith('.dc.html') else f + '.dc.html'
        if not os.path.exists(ROOT + '/project/' + f): continue
        sp = specs.get(f, {})
        st = sp.get('status') or ('已畫' if f in ('Avatar.dc.html', 'Mochi.dc.html', 'Sidebar.dc.html') else '待定')
        cnt[st] = cnt.get(st, 0) + 1; total += 1
        s = sp.get('simplified') or None
        items.append({'code': f[:-8].split('-')[0], 'file': f, 'title': sp.get('title') or f[:-8], 'what': (sp.get('what') or '')[:60], 'prd': '、'.join(sp.get('prd') or []) or '—', 'st': st,
                      'stBg': {'已畫': '#1F3B2C', '簡化過': '#14304F', '待定': '#3D2F12'}.get(st, '#262626'), 'stFg': {'已畫': '#38D591', '簡化過': '#7FC0FF', '待定': '#FFB547'}.get(st, '#FCFCFC'),
                      'simp': (str(s.get('before', '')) + ' → ' + str(s.get('after', ''))) if s else ''})
    groups.append({'title': title, 'items': items})
H = 260 + sum(64 + 40 * len(g['items']) for g in groups)
data = json.dumps({'groups': groups, 'total': total, 'drawn': cnt.get('已畫', 0), 'simp': cnt.get('簡化過', 0), 'pending': cnt.get('待定', 0)}, ensure_ascii=False)
html = '''<!doctype html>
<html lang="zh-Hant-TW">
<head>
<meta charset="utf-8">
<title>XBot 規格總覽索引</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Huninn&amp;family=JetBrains+Mono:wght@400;500&amp;display=swap">
<style>
body{margin:0;font-synthesis:none;background:#070707}
a{color:#7FC0FF;text-decoration:none}a:hover{text-decoration:underline}
</style>
</helmet>
<div style="width: 1440px; height: __H__px; box-sizing: border-box; padding: 48px 56px; background: #070707; color: #FCFCFC; font-family: 'Huninn', 'PingFang TC', 'Noto Sans TC', sans-serif; display: flex; flex-direction: column; gap: 24px">
<header style="display: flex; align-items: flex-end; gap: 20px">
<dc-import name="Mochi" size="64" pose="happy" hint-size="64px,64px"></dc-import>
<div style="flex: 1; display: flex; flex-direction: column; gap: 6px">
<h1 style="margin: 0; font-size: 32px; font-weight: 400">XBot 規格總覽索引</h1>
<span style="font-size: 14.5px; color: #FCFCFC99">功能 ↔ 畫板 ↔ PRD 章節 ↔ 完成狀態。點畫板編號（Play 模式）直接跳過去；每張畫板右邊的藍色便利貼是它的規格卡。</span>
</div>
<div style="display: flex; gap: 10px">
<span style="background: #111111; border-radius: 14px; padding: 10px 16px; display: flex; flex-direction: column"><span style="font-size: 24px">{{total}}</span><span style="font-size: 12.5px; color: #FCFCFC99">張畫板</span></span>
<span style="background: #111111; border-radius: 14px; padding: 10px 16px; display: flex; flex-direction: column"><span style="font-size: 24px; color: #38D591">{{drawn}}</span><span style="font-size: 12.5px; color: #FCFCFC99">已畫</span></span>
<span style="background: #111111; border-radius: 14px; padding: 10px 16px; display: flex; flex-direction: column"><span style="font-size: 24px; color: #7FC0FF">{{simp}}</span><span style="font-size: 12.5px; color: #FCFCFC99">簡化過</span></span>
<span style="background: #111111; border-radius: 14px; padding: 10px 16px; display: flex; flex-direction: column"><span style="font-size: 24px; color: #FFB547">{{pending}}</span><span style="font-size: 12.5px; color: #FCFCFC99">待定</span></span>
</div>
</header>
<div style="display: grid; grid-template-columns: 90px 300px 1fr 230px 110px 120px; gap: 0 16px; padding: 0 16px; font-size: 12.5px; color: #FCFCFC90"><span>畫板</span><span>功能</span><span>說明</span><span>PRD 章節</span><span>簡化</span><span>狀態</span></div>
<sc-for list="{{groups}}" as="g" hint-placeholder-count="12">
<section style="background: #111111; border-radius: 16px; padding: 12px 16px; display: flex; flex-direction: column">
<h2 style="margin: 0 0 6px; font-size: 16px; font-weight: 400; color: #FCFCFC">{{g.title}}</h2>
<sc-for list="{{g.items}}" as="i" hint-placeholder-count="6">
<div style="display: grid; grid-template-columns: 90px 300px 1fr 230px 110px 120px; gap: 0 16px; align-items: center; min-height: 40px; border-top: 1px solid #262626; font-size: 14px">
<a href="{{i.file}}" style="font-family: 'JetBrains Mono', monospace; font-size: 13px">{{i.code}}</a>
<span style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{{i.title}}</span>
<span style="color: #FCFCFC99; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{{i.what}}</span>
<span style="color: #FCFCFC99; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{{i.prd}}</span>
<span style="color: #7FC0FF; font-size: 13px">{{i.simp}}</span>
<span style="justify-self: start; border-radius: 999px; padding: 2px 10px; font-size: 12.5px; background: {{i.stBg}}; color: {{i.stFg}}">{{i.st}}</span>
</div>
</sc-for>
</section>
</sc-for>
</div>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{"$preview":{"width":1440,"height":__H__}}'>
class Component extends DCLogic {
renderVals() {
return __DATA__;
}
}
</script>
</body>
</html>
'''
open(ROOT + '/project/X01-Index.dc.html', 'w', encoding='utf-8').write(html.replace('__H__', str(H)).replace('__DATA__', data))
print('index rows', total, 'height', H, cnt)
