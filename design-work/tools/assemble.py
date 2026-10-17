#!/usr/bin/env python3
"""Lay out canvas.json with a spec sticky beside every artboard, and build X01-Index from specs/*.json.
usage: assemble.py <heights.json from shot.mjs --noshot ALL>"""
import json, os, sys, glob, re

ROOT = '/tmp/claude-1000/-home-daniel-Projects/119fe167-bf85-4b35-9f2a-d88d25ba20e9/scratchpad/xbot-design'
P = ROOT + '/project'
canvas = json.load(open(P + '/canvas.json', encoding='utf-8'))
heights = {}
if len(sys.argv) > 1:
    for r in json.load(open(sys.argv[1])):
        heights[r['file']] = (r['W'], r['H'])

specs = {}
for f in glob.glob(ROOT + '/specs/*.json'):
    try:
        d = json.load(open(f, encoding='utf-8'))
    except Exception as e:
        print('BAD SPEC', f, e); continue
    name = d.get('file') or os.path.basename(f)[:-5] + '.dc.html'
    if not name.endswith('.dc.html'): name += '.dc.html'
    specs[name] = d

ROWS = [
 ('X', '總覽索引與風格確認', ['X01-Index.dc.html', 'Z01-GrokStyle.dc.html']),
 ('A', 'A · 入口與首次使用（6 步 → 2 步）', ['A01-Login', 'A02-States', 'A03-Onboard-Hello', 'A04-Onboard-Engines', 'A05-Onboard-Import', 'A06-Onboard-Computer', 'A07-Onboard-FirstBot', 'A08-Onboard-Phone', 'A09-Welcome-Empty', 'A10-Tour']),
 ('B', 'B · 工作區、待我處理、通知　K · 語音', ['Main', 'B02-Sidebar-Menus', 'B03-CommandPalette', 'B04-Notifications', 'B05-Shortcuts', 'B06-Archived', 'B07-Sidebar-Search', 'B08-Toasts-Confirm', 'K01-VoiceCall', 'K02-VoiceSetup']),
 ('C', 'C · 和 Bot 對話', ['C01-Chat-Working', 'C02-Chat-Answer', 'C03-Chat-Empty', 'C04-Chat-Dark', 'C05-Composer', 'C06-ModelPicker', 'C07-Markdown', 'C08-Cards-Approval', 'C09-Cards-Questions', 'C10-Cards-Credentials', 'C11-Cards-Runs', 'C12-Progress-Context', 'C13-PeerDialog', 'C14-Events', 'C15-PreCheck', 'C16-MCPAttribution']),
 ('D', 'D · 群組對話：Bot 互聊', ['D01-Room', 'D02-Room-Goal', 'D03-RoomDialog', 'D04-RoomSetup', 'D05-GroupCall']),
 ('P', 'P · 說明：雲端 vs 地端、Tailscale、Bot 互談限制、任務完成、快判模型、MCP', ['P01-CloudVsLocal', 'P02-Tailscale', 'P03-BotTalkLimits', 'P04-TaskDone', 'P05-JudgeMap', 'P06-JudgeUses', 'P07-MCPArchitecture']),
 ('E', 'E · Bot 的電腦（這台主機與你的雲端主機）　M · 檔案', ['E01-Computer', 'E02-Takeover', 'E03-Office', 'E04-Teaching', 'E05-TeachStart', 'E06-Browser', 'E07-Files', 'E08-Data', 'E09-LoginShare', 'E10-Computer-States', 'M01-FilesPage']),
 ('F', 'F · 團隊與組織', ['F01-Org', 'F02-TeamBuilder-1', 'F03-TeamBuilder-2', 'F04-TeamMap', 'F05-TeamMemory', 'F06-TeamApply', 'F07-ShareTeam', 'F08-Approvals', 'F09-Library', 'F10-ImportReview']),
 ('G', 'G · 建立與設定 Bot（12 分頁 → 4 分頁）', ['G01-New', 'G02-Overview', 'G03-Identity', 'G04-AvatarUpload', 'G05-Soul', 'G06-Skills', 'G07-Memory', 'G08-Access', 'G09-Model', 'G10-Permissions', 'G11-Voice', 'G12-History', 'G13-ShareBot', 'G14-ImportBot']),
 ('H', 'H · 行事曆、定時任務、觸發', ['H01-Calendar', 'H02-RoutineEditor', 'H03-RoutineList', 'H04-Agenda', 'H05-Triggers', 'H06-Webhook']),
 ('I', 'I · 連接應用程式與登入', ['I01-Apps', 'I02-AppSheet', 'I03-SignIn', 'I04-MCP', 'I05-Logins']),
 ('J', 'J · 設定（15 區塊 → 5 個）＋快判模型、MCP', ['J01-General', 'J02-Models', 'J03-Routing', 'J04-Usage', 'J05-Devices', 'J06-Computers', 'J07-Backup', 'J08-People', 'J09-SkillsLab', 'J10-FastJudge', 'J11-AddHost', 'J12-MCPAccess']),
 ('L', 'L · 手機', ['L01-PhoneList', 'L02-PhoneInbox', 'L03-PhoneChat', 'L04-PhoneComputer', 'L05-PhoneRoom', 'L06-PhoneSettings', 'L07-PhoneSheet', 'L08-PhoneOnboard', 'L09-PhonePair', 'L10-PhoneBotProfile', 'L11-PhoneAvatarSheet', 'L12-PhoneNewBot', 'L13-PhoneNewRoom', 'L14-PhoneNotifications', 'L15-PhoneOffice', 'L16-PhoneCalendar', 'L17-PhoneRoutine', 'L18-PhoneCall', 'L19-PhoneFiles', 'L20-PhoneMentionStop', 'L21-PhonePreCheck']),
 ('N', 'N · 設計系統：Grok 風格、游標頭像', ['N01-Tokens', 'N02-Mascot', 'N03-Components', 'Avatar', 'Mochi', 'Sidebar']),
]
NOTE_W = 380
GAP = 40
ROW_GAP = 420

old = canvas['boards']
boards, order, notes = {}, [], {}
y = 0
for key, title, files in ROWS:
    files = [f if f.endswith('.dc.html') else f + '.dc.html' for f in files]
    files = [f for f in files if os.path.exists(P + '/' + f)]
    if not files: continue
    notes['row' + key] = {"x": 0, "y": y - 300, "text": title, "kind": "title1"}
    x = 0; rowh = 0
    for f in files:
        b = dict(old.get(f, {}))
        w = b.get('w', 1440); h = b.get('h', 960)
        if f in heights:
            rw, rh = heights[f]
            if rh > 40: h = rh
        if f in heights and heights[f][0] > 0 and b.get('expand') != 'fill':
            w = heights[f][0]
        b.update({"x": x, "y": y, "w": w, "h": h})
        src = open(P + '/' + f, encoding='utf-8').read()
        if 'onClick=' in src: b['is_interactive'] = True
        else: b.pop('is_interactive', None)
        sp = specs.get(f)
        if sp and sp.get('title'): b['title'] = f[:-8].split('-')[0] + ' ' + sp['title']
        boards[f] = b; order.append(f)
        x += w + GAP
        if sp:
            lines = []
            prd = '、'.join(sp.get('prd') or [])
            lines.append('【規格】' + (sp.get('title') or f) + ('　PRD ' + prd if prd else ''))
            if sp.get('what'): lines.append('功能：' + sp['what'])
            if sp.get('goal'): lines.append('使用者要完成：' + sp['goal'])
            if sp.get('accept'): lines.append('驗收：\n' + '\n'.join('· ' + a for a in sp['accept']))
            if sp.get('states'): lines.append('狀態：' + '、'.join(sp['states']))
            if sp.get('edge'): lines.append('例外：\n' + '\n'.join('· ' + a for a in sp['edge']))
            if sp.get('limits'): lines.append('限制：\n' + '\n'.join('· ' + a for a in sp['limits']))
            s = sp.get('simplified')
            if s: lines.append('簡化：' + str(s.get('flow', '')) + ' ' + str(s.get('before', '')) + ' → ' + str(s.get('after', '')) + ('（' + s['how'] + '）' if s.get('how') else ''))
            lines.append('完成狀態：' + (sp.get('status') or '已畫'))
            nid = 'spec-' + re.sub(r'[^A-Za-z0-9_-]', '', f[:-8])[:34]
            notes[nid] = {"x": x, "y": y, "text": '\n\n'.join(lines), "w": NOTE_W, "maxH": max(h, 600), "fill": "blue"}
            x += NOTE_W + 120
        else:
            x += 80
        rowh = max(rowh, h, 600 if sp else 0)
    notes['row' + key]['maxW'] = max(x - 120, 1440)
    y += rowh + ROW_GAP

canvas['boards'] = boards
canvas['order'] = ['X01-Index.dc.html'] + [o for o in order if o != 'X01-Index.dc.html'] if 'X01-Index.dc.html' in boards else order
canvas['notes'] = notes
json.dump(canvas, open(P + '/canvas.json', 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
missing = [f for f in boards if f not in specs and f not in ('Avatar.dc.html', 'Mochi.dc.html', 'Sidebar.dc.html', 'X01-Index.dc.html', 'Z01-GrokStyle.dc.html')]
print('boards', len(boards), 'notes', len(notes), 'specs', len(specs), 'missing specs', missing)
extra = [os.path.basename(f) for f in glob.glob(P + '/*.dc.html') if os.path.basename(f) not in boards]
print('files not on canvas', extra)
