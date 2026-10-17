import { I, page, header, backBtn, SAFE_TOP } from './lib.mjs';

const row = `
<article style="flex: none; display: flex; gap: 12px; padding: 12px 16px; border-bottom: 1px solid #191919">
<span style="flex: none; padding-top: 2px"><dc-import name="Avatar" color="{{n.color}}" state="{{n.state}}" dot="{{n.dot}}" ring="#070707" size="40" hint-size="40px,40px"></dc-import></span>
<div style="flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px">
<div style="display: flex; align-items: center; gap: 6px">
<span style="font-size: 15px">{{n.who}}</span>
<span style="font-size: 12.5px; color: {{n.kindInk}}">{{n.kind}}</span>
<span style="margin-left: auto; flex: none; font-size: 12.5px; color: #FCFCFC90">{{n.time}}</span>
<sc-if value="{{n.unread}}" hint-placeholder-val="{{ false }}"><span aria-label="未讀" style="width: 8px; height: 8px; border-radius: 9px; background: #1084FE; flex: none"></span></sc-if>
</div>
<div style="display: flex; align-items: center; gap: 10px">
<div style="flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px">
<p style="margin: 0; font-size: 14.5px; line-height: 1.5; color: #FCFCFCE6">{{n.text}}</p>
<sc-if value="{{n.hasCtx}}" hint-placeholder-val="{{ false }}"><span style="font-size: 12.5px; color: #FCFCFC90">{{n.ctx}}</span></sc-if>
</div>
<sc-if value="{{n.hasLink}}" hint-placeholder-val="{{ false }}">
<a href="{{n.href}}" style="flex: none; height: 44px; padding: 0 16px; box-sizing: border-box; border: 1px solid #333333; border-radius: 999px; color: #FCFCFC; font-size: 14px; display: inline-flex; align-items: center">{{n.linkText}}</a>
</sc-if>
</div>

<sc-if value="{{n.isAsk}}" hint-placeholder-val="{{ false }}">
<div style="display: flex; gap: 8px; margin-top: 8px">
<button type="button" onClick="{{n.allow}}" style="height: 44px; padding: 0 18px; border: 0; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 14.5px; cursor: pointer">允許這次</button>
<button type="button" onClick="{{n.reject}}" style="height: 44px; padding: 0 18px; border: 0; border-radius: 999px; background: #2F2F2F; color: #FCFCFC; font-size: 14.5px; cursor: pointer">退回</button>
</div>
</sc-if>
<sc-if value="{{n.isDecided}}" hint-placeholder-val="{{ false }}">
<div role="status" style="display: flex; align-items: center; gap: 8px; margin-top: 4px; min-height: 44px; font-size: 14px; color: {{n.decInk}}"><span style="display: flex">${I('circle-check', 18)}</span><span style="flex: 1">{{n.decText}}</span><button type="button" onClick="{{n.undo}}" style="height: 44px; padding: 0 10px; border: 0; background: transparent; color: #7FC0FF; font-size: 14px; cursor: pointer">復原</button></div>
</sc-if>


<sc-if value="{{n.isExpired}}" hint-placeholder-val="{{ false }}">
<div style="margin-top: 6px; background: #262626; border-radius: 18px; padding: 12px 14px; display: flex; flex-direction: column; gap: 6px">
<span style="font-size: 15px; color: #FCFCFC99">要不要退訂這 12 份電子報？</span>
<span style="display: flex; align-items: center; gap: 8px; font-size: 13.5px; color: #FFB547"><span style="display: flex">${I('circle-alert', 16)}</span>10 分鐘沒回，信差先跳過了</span>
<div style="display: flex; align-items: center; gap: 8px"><span style="flex: 1; display: flex; align-items: center; gap: 8px; font-size: 13.5px; color: #FCFCFC90"><span style="display: flex">${I('clock-alert', 16)}</span>已過期</span>
<button type="button" onClick="{{n.reask}}" style="flex: none; height: 44px; padding: 0 18px; border: 1px solid #333333; border-radius: 999px; background: transparent; color: #FCFCFC; font-size: 14.5px; cursor: pointer">{{n.reaskText}}</button></div>
</div>
</sc-if>
</div>
</article>`;

const body = `${SAFE_TOP}
${header(backBtn('L02-PhoneInbox.dc.html'), '通知', '<button type="button" onClick="{{readAll}}" style="height: 44px; padding: 0 10px; border: 0; background: transparent; color: {{readAllInk}}; font-size: 14.5px; display: flex; align-items: center; gap: 6px; cursor: pointer">' + I('check-check', 18) + '全部標為已讀</button>')}
<div role="tablist" aria-label="篩選" style="flex: none; display: flex; gap: 6px; padding: 0 12px 8px; overflow-x: auto">
<sc-for list="{{filters}}" as="f" hint-placeholder-count="5">
<button type="button" role="tab" aria-selected="{{f.on}}" onClick="{{f.pick}}" style="flex: none; height: 44px; padding: 0; border: 0; background: transparent; cursor: pointer; display: flex; align-items: center">
<span style="height: 34px; padding: 0 12px; border-radius: 999px; background: {{f.bg}}; border: 1px solid {{f.line}}; color: {{f.ink}}; font-size: 14px; display: flex; align-items: center; gap: 5px; box-sizing: border-box; white-space: nowrap">{{f.label}}<sc-if value="{{f.hasN}}" hint-placeholder-val="{{ false }}"><span style="font-size: 12px; color: {{f.nInk}}">{{f.n}}</span></sc-if></span>
</button>
</sc-for>
</div>
<div style="flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column">
<sc-for list="{{groups}}" as="g" hint-placeholder-count="2">
<h2 style="flex: none; margin: 0; padding: 10px 16px 4px; font-size: 13px; font-weight: 400; color: #FCFCFC90">{{g.title}}</h2>
<sc-for list="{{g.items}}" as="n" hint-placeholder-count="3">${row}
</sc-for>
</sc-for>
<sc-if value="{{empty}}" hint-placeholder-val="{{ false }}">
<p style="margin: 0; padding: 48px 24px; text-align: center; font-size: 14.5px; color: #FCFCFC99">這裡沒有通知</p>
</sc-if>
</div>
<div aria-hidden="true" style="height: 34px; flex: none"></div>`;

const script = `
class Component extends DCLogic {
renderVals() {
const st = this.state || {};
const f = st.f || '全部';
const read = !!st.read;
const dec = st.dec || '';
const reask = !!st.reask;
const K = { ask: '要你決定', done: '完成', at: '提到你', err: '出錯' };
const INK = { ask: '#FFB547', done: '#38D591', at: '#7FC0FF', err: '#FF5667' };
const base = [
{ g: '今天', id: 'pay', k: 'ask', who: '帳房', color: 'yellow', state: dec === 'ok' ? 'happy' : 'notifying', dot: dec ? 'none' : 'waiting', time: '下午 2:55', text: '想付 10 月廣告費 NT$1,280 給晨光廣告。', ctx: '新增支出只有你能決定', isAsk: !dec, isDecided: !!dec },
{ g: '今天', id: 'otp', k: 'ask', who: '小編', color: 'pink', state: 'notifying', dot: 'waiting', time: '下午 2:47', text: '要輸入驗證碼，請你接手電腦。', hasLink: true, href: 'L04-PhoneComputer.dc.html', linkText: '接手' },
{ g: '今天', id: 'exp', k: 'ask', who: '信差', color: 'cyan', state: 'sleeping', dot: 'none', time: '下午 12:30', text: '整理收件匣時問了你：', isExpired: true, kindX: '提問', inkX: '#FCFCFC90' },
{ g: '今天', id: 'at', k: 'at', who: '阿哲', color: 'orange', state: 'searching', dot: 'none', time: '下午 2:40', text: '@你 價格抓好了，要看嗎？', ctx: '新品上架專案', hasLink: true, href: 'L05-PhoneRoom.dc.html', linkText: '打開' },
{ g: '今天', id: 'err', k: 'err', who: '抓蟲', color: 'red', state: 'alerting', dot: 'error', time: '下午 1:12', text: '測試網站連不上，先停下來了。', ctx: '已自動重試 3 次', hasLink: true, href: 'L03-PhoneChat.dc.html', linkText: '查看' },
{ g: '今天', id: 'done', k: 'done', who: '阿凱', color: 'green', state: 'idle', dot: 'none', time: '上午 10:42', text: '3 封開發信草稿寫好了，等你看過再寄。' }
];
const unreadIds = { pay: 1, otp: 1, at: 1, err: 1 };
const items = base.filter((n) => f === '全部' || K[n.k] === f).map((n) => Object.assign({ hasLink: false, isAsk: false, isDecided: false, isExpired: false, ctx: '' }, n, {
kind: n.kindX || K[n.k], kindInk: n.inkX || INK[n.k], unread: !read && !!unreadIds[n.id] && !(n.id === 'pay' && dec), hasCtx: !!n.ctx && !(n.id === 'pay' && dec),
decText: dec === 'ok' ? '已允許這次 · 帳房開始付款' : '已退回 · 帳房會告訴小秘', decInk: dec === 'ok' ? '#38D591' : '#FCFCFC99',
allow: () => this.setState({ dec: 'ok' }), reject: () => this.setState({ dec: 'no' }), undo: () => this.setState({ dec: '' }),
reask: () => this.setState({ reask: true }), reaskText: reask ? '已請它再問' : '請它再問一次'
}));
const groups = ['今天'].map((title) => ({ title, items: items.filter((n) => n.g === title) })).filter((g) => g.items.length);
const counts = { '全部': 0 };
base.forEach((n) => { counts[K[n.k]] = (counts[K[n.k]] || 0) + 1; counts['全部'] += 1; });
const filters = ['全部', '要你決定', '完成', '提到你', '出錯'].map((label) => ({
label, on: label === f, n: counts[label] || 0, hasN: label !== '全部',
bg: label === f ? '#FCFCFC' : 'transparent', ink: label === f ? '#070707' : '#FCFCFC', line: label === f ? '#FCFCFC' : '#333333', nInk: label === f ? '#070707B0' : '#FCFCFC90',
pick: () => this.setState({ f: label })
}));
return { filters, groups, empty: !groups.length, readAllInk: read ? '#FCFCFC90' : '#7FC0FF', readAll: () => this.setState({ read: true }) };
}
}`;

console.log('L14', page({ file: 'L14-PhoneNotifications.dc.html', title: '手機：通知', body, script }));
