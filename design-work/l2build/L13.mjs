import { I, page, header, backBtn, tagRow, SAFE_TOP } from './lib.mjs';

const pickList = `
<sc-if value="{{isPick}}" hint-placeholder-val="{{ true }}">
<div style="flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column">
<p style="flex: none; margin: 0; padding: 0 20px 8px; font-size: 14px; color: #FCFCFC99">勾 2–10 個 Bot。名字和回應規則會自動設好。</p>
<sc-for list="{{bots}}" as="b" hint-placeholder-count="8">
<label style="flex: none; position: relative; height: 56px; display: flex; align-items: center; gap: 12px; padding: 0 20px 0 16px; cursor: pointer; background: {{b.rowBg}}">
<input type="checkbox" checked="{{b.on}}" onChange="{{b.flip}}" style="position: absolute; opacity: 0; width: 1px; height: 1px">
<dc-import name="Avatar" color="{{b.color}}" state="{{b.state}}" dot="{{b.dot}}" ring="#070707" size="40" hint-size="40px,40px"></dc-import>
<span style="flex: 1; min-width: 0; display: flex; flex-direction: column">
<span style="font-size: 15.5px">{{b.name}}</span>
<span style="font-size: 13px; color: {{b.subInk}}; white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{{b.sub}}</span>
</span>
<span aria-hidden="true" style="width: 22px; height: 22px; box-sizing: border-box; border-radius: 6px; flex: none; background: {{b.boxBg}}; border: {{b.boxLine}}; color: #FFFFFF; display: flex; align-items: center; justify-content: center"><sc-if value="{{b.on}}" hint-placeholder-val="{{ false }}">${I('check', 15, 'currentColor', 2.5)}</sc-if></span>
</label>
</sc-for>
</div>
<div style="flex: none; padding: 10px 16px 0; border-top: 1px solid #262626; display: flex; flex-direction: column; gap: 8px">
<div style="display: flex; align-items: center; gap: 8px; min-height: 30px">
<span style="display: flex; align-items: center"><sc-for list="{{chosen}}" as="c" hint-placeholder-count="3"><span style="margin-right: -6px; display: flex"><dc-import name="Avatar" color="{{c.color}}" state="idle" dot="none" ring="#070707" size="26" hint-size="26px,26px"></dc-import></span></sc-for></span>
<span style="flex: 1; margin-left: 10px; font-size: 13.5px; color: #FCFCFC99">已選 {{count}} 位</span>
<span style="font-size: 12.5px; color: {{hintInk}}">{{hint}}</span>
</div>
<button type="button" onClick="{{next}}" disabled="{{cantNext}}" style="height: 48px; border: 0; border-radius: 999px; background: {{nextBg}}; color: {{nextInk}}; font-size: 15.5px; cursor: pointer">下一步</button>
</div>
</sc-if>`;

const confirm = `
<sc-if value="{{isConfirm}}" hint-placeholder-val="{{ false }}">
<div style="flex: 1; min-height: 0; padding: 4px 16px 0; display: flex; flex-direction: column; gap: 16px">
<div style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding-top: 8px">
<span style="display: flex; align-items: center"><sc-for list="{{chosen}}" as="c" hint-placeholder-count="3"><span style="margin: 0 -8px; display: flex"><dc-import name="Avatar" color="{{c.color}}" state="happy" dot="none" ring="#070707" size="52" hint-size="52px,52px"></dc-import></span></sc-for></span>
<span style="font-size: 14px; color: #FCFCFC99">{{count}} 位成員</span>
</div>
<label style="display: flex; flex-direction: column; gap: 6px">
<span style="display: flex; align-items: center; gap: 6px; font-size: 13px; color: #FCFCFC99">群組名稱<span style="height: 20px; display: inline-flex; align-items: center; gap: 3px; padding: 0 7px; border-radius: 999px; background: #2F2F2F; font-size: 11.5px">${I('sparkles', 11)}自動</span></span>
<input type="text" value="{{roomName}}" readonly style="height: 44px; box-sizing: border-box; padding: 0 14px; border: 1px solid #333333; border-radius: 10px; background: #191919; color: #FCFCFC; font: inherit; font-size: 15px; outline: none">
</label>
<div style="background: #262626; border-radius: 14px; padding: 12px 14px; display: flex; flex-direction: column; gap: 2px">
<span style="display: flex; align-items: center; gap: 8px"><span style="flex: 1; font-size: 15px">誰回答</span><span style="font-size: 14px; color: #FCFCFC99">自動挑一位</span></span>
<span style="font-size: 13px; color: #FCFCFC90">沒點名時，系統挑最適合的一位回答。用 @ 可以點名。</span>
</div>
<p style="margin: 0; padding: 0 2px; font-size: 13px; color: #FCFCFC90">名稱、成員、回應規則都可以在群組裡改。</p>
<div style="margin-top: auto; display: flex; flex-direction: column; gap: 6px">
<a href="L05-PhoneRoom.dc.html" style="height: 48px; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; display: flex; align-items: center; justify-content: center">建立</a>
<button type="button" onClick="{{back}}" style="height: 44px; border: 0; background: transparent; color: #FCFCFC99; font-size: 14.5px; cursor: pointer">改成員</button>
</div>
</div>
</sc-if>`;

const body = `${SAFE_TOP}
${header(backBtn('L01-PhoneList.dc.html'), '{{title}}', '<span style="font-size: 13px; color: #FCFCFC99; padding-right: 6px">{{stepNo}}/2</span>')}
${tagRow('5 個欄位 → 2 步')}
${pickList}
${confirm}
<div aria-hidden="true" style="height: 34px; flex: none"></div>`;

const script = `
class Component extends DCLogic {
renderVals() {
const st = this.state || {};
const step = st.step || 1;
const sel = Object.assign({ xm: true, az: true, xb: true }, st.sel || {});
const all = [
['xm', '小秘', 'blue', 'working', 'none', '幕僚長 · 工作中'],
['az', '阿哲', 'orange', 'searching', 'none', '選品研究員 · 工作中'],
['xb', '小編', 'pink', 'notifying', 'waiting', '社群小編 · 等你回覆'],
['ak', '阿凱', 'green', 'idle', 'none', '業務開發 · 待命'],
['zf', '帳房', 'yellow', 'notifying', 'waiting', '費用管理 · 等你允許'],
['sj', '數據喵', 'purple', 'idle', 'none', '數據分析師 · 排隊中'],
['xc', '信差', 'cyan', 'sleeping', 'none', '收件匣管家 · 休息'],
['zc', '抓蟲', 'red', 'idle', 'none', 'Bug 重現 · 待命']
];
const count = all.filter((a) => sel[a[0]]).length;
const bots = all.map(([id, name, color, state, dot, sub]) => {
const on = !!sel[id];
const full = !on && count >= 10;
return { name, color, state, dot, sub, on,
subInk: dot === 'waiting' ? '#FFB547' : '#FCFCFC99',
rowBg: on ? '#111111' : 'transparent',
boxBg: on ? '#1084FE' : 'transparent', boxLine: on ? '0' : '1.5px solid #5A5A5A',
flip: () => { if (!full) this.setState({ sel: Object.assign({}, sel, { [id]: !on }) }); } };
});
const chosen = all.filter((a) => sel[a[0]]).map((a) => ({ color: a[2], name: a[1] }));
const names = chosen.map((c) => c.name);
const roomName = names.length > 3 ? names.slice(0, 3).join('、') + ' 等 ' + names.length + ' 位' : names.join('、');
const ok = count >= 2 && count <= 10;
return {
isPick: step === 1, isConfirm: step === 2, stepNo: step,
title: step === 1 ? '新增群組：勾成員' : '新增群組：建立',
bots, chosen, count, roomName,
hint: count < 2 ? '至少 2 位' : '最多 10 位', hintInk: count < 2 ? '#FFB547' : '#FCFCFC90',
cantNext: !ok, nextBg: ok ? '#FCFCFC' : '#2F2F2F', nextInk: ok ? '#070707' : '#FCFCFC90',
next: () => { if (ok) this.setState({ step: 2 }); },
back: () => this.setState({ step: 1 })
};
}
}`;

console.log('L13', page({ file: 'L13-PhoneNewRoom.dc.html', title: '手機：新增群組', body, script }));
