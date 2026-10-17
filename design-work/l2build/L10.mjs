import { I, page, header, backBtn, tagRow, switchBtn, divider, SAFE_TOP } from './lib.mjs';

const seg = (list, item) => `<div role="radiogroup" style="display: flex; padding: 3px; border-radius: 10px; background: #191919; gap: 2px">
<sc-for list="{{${list}}}" as="${item}" hint-placeholder-count="3">
<button type="button" role="radio" aria-checked="{{${item}.on}}" onClick="{{${item}.pick}}" style="flex: 1; height: 44px; border: 0; border-radius: 8px; background: {{${item}.bg}}; color: {{${item}.ink}}; font-size: 14px; cursor: pointer; white-space: nowrap">{{${item}.label}}</button>
</sc-for>
</div>`;

const quick = (href, icon, label) => `<a href="${href}" style="flex: 1; height: 64px; border-radius: 14px; background: #262626; color: #FCFCFC; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; font-size: 13.5px">${I(icon, 22)}${label}</a>`;

const chev = `<span style="color: #FCFCFC90; display: flex">${I('chevron-right', 18)}</span>`;

const basic = `
<sc-if value="{{tBasic}}" hint-placeholder-val="{{ true }}">
<div style="background: #262626; border-radius: 14px">
<a href="G02-Overview.dc.html" style="min-height: 60px; display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 14px; box-sizing: border-box; color: #FCFCFC">
<span style="flex: 1; min-width: 0; display: flex; flex-direction: column"><span style="font-size: 13px; color: #FCFCFC99">它做什麼</span><span style="font-size: 15px">每天整理重點，把工作分給其他 Bot</span></span>${chev}</a>
${divider}
<div style="padding: 10px 14px 12px; display: flex; flex-direction: column; gap: 8px">
<span style="font-size: 13px; color: #FCFCFC99">回覆長度</span>
${seg('lens', 'l')}
</div>
${divider}
<div style="padding: 10px 14px 12px; display: flex; flex-direction: column; gap: 8px">
<span style="display: flex; align-items: baseline; gap: 8px"><span style="flex: 1; font-size: 13px; color: #FCFCFC99">放在哪台主機</span><span style="font-size: 12.5px; color: #FCFCFC90">{{hostNote}}</span></span>
${seg('hosts', 'h')}
</div>
</div>
</sc-if>`;

const caps = `
<sc-if value="{{tCaps}}" hint-placeholder-val="{{ false }}">
<div style="background: #262626; border-radius: 14px">
<a href="G06-Skills.dc.html" style="min-height: 60px; display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 14px; box-sizing: border-box; color: #FCFCFC"><span style="flex: 1; min-width: 0; display: flex; flex-direction: column"><span style="font-size: 13px; color: #FCFCFC99">技能</span><span style="font-size: 15px">每日重點、分派工作、會議紀錄</span></span>${chev}</a>
${divider}
<a href="G07-Memory.dc.html" style="min-height: 60px; display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 14px; box-sizing: border-box; color: #FCFCFC"><span style="flex: 1; min-width: 0; display: flex; flex-direction: column"><span style="font-size: 13px; color: #FCFCFC99">記憶</span><span style="font-size: 15px">記得 18 件事 · 自動整理</span></span>${chev}</a>
${divider}
<a href="I01-Apps.dc.html" style="min-height: 60px; display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 14px; box-sizing: border-box; color: #FCFCFC"><span style="flex: 1; min-width: 0; display: flex; flex-direction: column"><span style="font-size: 13px; color: #FCFCFC99">應用程式</span><span style="font-size: 15px">Gmail、Google 行事曆、Notion</span></span>${chev}</a>
${divider}
<a href="L04-PhoneComputer.dc.html" style="min-height: 60px; display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px 14px; box-sizing: border-box; color: #FCFCFC"><span style="flex: 1; min-width: 0; display: flex; flex-direction: column"><span style="font-size: 13px; color: #FCFCFC99">電腦</span><span style="font-size: 15px">開著 · 閒置 30 分鐘休眠</span></span>${chev}</a>
</div>
</sc-if>`;

const perms = `
<sc-if value="{{tPerm}}" hint-placeholder-val="{{ false }}">
<div style="display: flex; flex-direction: column; gap: 6px">
<h2 style="margin: 0; padding-left: 14px; font-size: 13px; font-weight: 400; color: #FCFCFC90">這些事先問我</h2>
<div style="background: #262626; border-radius: 14px">
<sc-for list="{{asks}}" as="w" hint-placeholder-count="5">
<div style="height: 48px; display: flex; align-items: center; gap: 8px; padding: 0 6px 0 14px; border-top: {{w.line}}">
<span style="flex: 1; font-size: 15px">{{w.label}}</span>
${switchBtn('w.on', 'w.track', 'w.knob', 'w.flip', '{{w.label}}')}
</div>
</sc-for>
</div>
<a href="J04-Usage.dc.html" style="margin-top: 6px; min-height: 64px; display: flex; flex-direction: column; justify-content: center; gap: 8px; padding: 0 14px; border-radius: 14px; background: #262626; color: #FCFCFC">
<span style="display: flex; align-items: baseline; gap: 8px"><span style="flex: 1; font-size: 15px">花費上限</span><span style="font-size: 14px">NT$620</span><span style="font-size: 13px; color: #FCFCFC99">／每月 NT$2,000</span></span>
<span role="img" aria-label="本月已用 31%" style="height: 4px; border-radius: 9px; background: #333333; display: block; overflow: hidden"><span style="display: block; width: 31%; height: 4px; background: #1084FE"></span></span>
</a>
</div>
</sc-if>`;

const adv = `
<sc-if value="{{tAdv}}" hint-placeholder-val="{{ false }}">
<div style="background: #262626; border-radius: 14px">
<a href="G09-Model.dc.html" style="height: 52px; display: flex; align-items: center; gap: 10px; padding: 0 10px 0 14px; color: #FCFCFC"><span style="flex: 1; font-size: 15px">模型</span><span style="font-size: 14px; color: #FCFCFC99">Claude Sonnet 5.5</span>${chev}</a>
${divider}
<a href="G11-Voice.dc.html" style="height: 52px; display: flex; align-items: center; gap: 10px; padding: 0 10px 0 14px; color: #FCFCFC"><span style="flex: 1; font-size: 15px">聲音與通知</span><span style="font-size: 14px; color: #FCFCFC99">溫和女聲</span>${chev}</a>
${divider}
<a href="G10-Permissions.dc.html" style="height: 52px; display: flex; align-items: center; gap: 10px; padding: 0 10px 0 14px; color: #FCFCFC"><span style="flex: 1; font-size: 15px">誰看得到</span><span style="font-size: 14px; color: #FCFCFC99">只有你</span>${chev}</a>
${divider}
<a href="G12-History.dc.html" style="height: 52px; display: flex; align-items: center; gap: 10px; padding: 0 10px 0 14px; color: #FCFCFC"><span style="flex: 1; font-size: 15px">歷史與用量</span>${chev}</a>
</div>
</sc-if>`;

const body = `${SAFE_TOP}
${header(backBtn('L03-PhoneChat.dc.html'), 'Bot 資料')}
${tagRow('12 個分頁 → 4 個')}
<div style="flex: 1; min-height: 0; overflow-y: auto; padding: 0 16px 12px; display: flex; flex-direction: column; gap: 14px">
<div style="flex: none; display: flex; flex-direction: column; align-items: center; gap: 2px">
<a href="L11-PhoneAvatarSheet.dc.html" aria-label="換頭像" style="position: relative; display: block; width: 88px; height: 88px">
<dc-import name="Avatar" color="blue" state="working" dot="none" ring="#070707" size="88" hint-size="88px,88px"></dc-import>
<span aria-hidden="true" style="position: absolute; right: -6px; bottom: -2px; width: 30px; height: 30px; box-sizing: border-box; border-radius: 999px; background: #2F2F2F; border: 2px solid #070707; color: #FCFCFC; display: flex; align-items: center; justify-content: center">${I('square-pen', 15)}</span>
</a>
<div style="margin-top: 8px; display: flex; align-items: center; gap: 6px"><span style="font-size: 22px; line-height: 1.3">小秘</span><span role="img" aria-label="幕僚長" style="color: #FCFCFC99; display: flex">${I('crown', 18)}</span></div>
<span style="font-size: 14px; color: #FCFCFC99">幕僚長 · 每日重點與協調</span>
<span style="font-size: 13px; color: #FCFCFC90">工作中 · 彙整三家競品定價</span>
</div>
<div style="flex: none; display: flex; gap: 8px">
${quick('L03-PhoneChat.dc.html', 'message-circle', '對話')}
${quick('L04-PhoneComputer.dc.html', 'monitor', '電腦')}
${quick('K01-VoiceCall.dc.html', 'phone', '通話')}
</div>
<div role="tablist" aria-label="Bot 設定" style="flex: none; display: flex; padding: 3px; border-radius: 10px; background: #111111; gap: 2px">
<sc-for list="{{tabs}}" as="t" hint-placeholder-count="4">
<button type="button" role="tab" aria-selected="{{t.on}}" onClick="{{t.pick}}" style="flex: 1; height: 44px; padding: 0 4px; border: 0; border-radius: 8px; background: {{t.bg}}; color: {{t.ink}}; font-size: 14px; cursor: pointer; white-space: nowrap">{{t.label}}</button>
</sc-for>
</div>
<div role="tabpanel" style="flex: none; display: flex; flex-direction: column">
${basic}
${caps}
${perms}
${adv}
</div>
<p style="flex: none; margin: 0; text-align: center; font-size: 12.5px; color: #FCFCFC90">變更會自動儲存</p>
</div>
<div aria-hidden="true" style="height: 34px; flex: none"></div>`;

const script = `
class Component extends DCLogic {
renderVals() {
const st = this.state || {};
const tab = st.tab || '基本';
const len = st.len || '標準';
const host = st.host || '這台主機';
const ask = Object.assign({ a: true, b: true, c: true, d: true, e: true }, st.ask || {});
const S = (cur, key) => (label) => ({ label, on: label === cur, bg: label === cur ? '#2F2F2F' : 'transparent', ink: label === cur ? '#FCFCFC' : '#FCFCFC99', pick: () => this.setState({ [key]: label }) });
const asks = [['a', '新商品定價'], ['b', '新增支出'], ['c', '簽約'], ['d', '上架'], ['e', '重要對客承諾']].map(([id, label], i) => {
const on = !!ask[id];
return { label, on, line: i ? '1px solid #333333' : '0', track: on ? '#1084FE' : '#3D3D3D', knob: on ? '18px' : '2px', flip: () => this.setState({ ask: Object.assign({}, ask, { [id]: !on }) }) };
});
return {
tabs: ['基本', '能力', '權限與花費', '進階'].map(S(tab, 'tab')),
lens: ['精簡', '標準', '詳細'].map(S(len, 'len')),
hosts: ['這台主機', 'cloud-01'].map(S(host, 'host')),
hostNote: host === '這台主機' ? '辦公室的 Mac mini' : '你的雲端主機 · 已連線',
tBasic: tab === '基本', tCaps: tab === '能力', tPerm: tab === '權限與花費', tAdv: tab === '進階',
asks
};
}
}`;

console.log('L10', page({ file: 'L10-PhoneBotProfile.dc.html', title: '手機：Bot 資料', body, script }));
