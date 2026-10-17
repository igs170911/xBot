import { I, page, header, backBtn, tagRow, SAFE_TOP } from './lib.mjs';

const chip = (inner) => `<span style="height: 30px; display: inline-flex; align-items: center; gap: 6px; padding: 0 12px; border: 1px solid #333333; border-radius: 999px; font-size: 13.5px; white-space: nowrap">${inner}</span>`;

const card = `
<sc-if value="{{showCard}}" hint-placeholder-val="{{ true }}">
<section aria-labelledby="sug" style="background: #262626; border-radius: 18px; padding: 16px; display: flex; flex-direction: column; gap: 14px">
<div style="display: flex; align-items: center; gap: 12px">
<dc-import name="Avatar" color="teal" state="happy" dot="none" ring="#262626" size="52" hint-size="52px,52px"></dc-import>
<div style="flex: 1; min-width: 0; display: flex; flex-direction: column">
<span id="sug" style="font-size: 17px">客服小蝦</span>
<span style="font-size: 13.5px; color: #FCFCFC99">蝦皮客服 · 回覆買家問題</span>
</div>
<span style="align-self: flex-start; height: 24px; display: inline-flex; align-items: center; gap: 4px; padding: 0 9px; border-radius: 999px; background: #2F2F2F; color: #FCFCFC99; font-size: 12px; white-space: nowrap">${I('sparkles', 13)}系統建議</span>
</div>
<div style="display: flex; flex-direction: column; gap: 8px">
<span style="font-size: 13px; color: #FCFCFC99">技能</span>
<div style="display: flex; flex-wrap: wrap; gap: 6px">${chip('回覆買家')}${chip('查訂單狀態')}${chip('整理常見問題')}</div>
</div>
<div style="display: flex; flex-direction: column; gap: 8px">
<span style="font-size: 13px; color: #FCFCFC99">要連的應用程式</span>
<div style="display: flex; flex-wrap: wrap; gap: 6px">${chip(`<span style="color: #FCFCFC99; display: flex">${I('link', 14)}</span>蝦皮賣家中心`)}${chip(`<span style="color: #FCFCFC99; display: flex">${I('link', 14)}</span>Gmail`)}</div>
</div>
<div style="display: flex; align-items: center; gap: 8px; padding-top: 12px; border-top: 1px solid #333333; font-size: 13.5px; color: #FCFCFC99">
<span style="display: flex">${I('shield-check', 16)}</span><span style="flex: 1">權限：先問我 · 退款、對客承諾前問你</span>
</div>
<p style="margin: -4px 0 0; font-size: 12.5px; color: #FCFCFC90">名字、人設、技能都可以之後在 Bot 資料裡改。</p>
</section>
</sc-if>`;

const done = `
<sc-if value="{{isDone}}" hint-placeholder-val="{{ false }}">
<div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 28px 0 8px; text-align: center">
<dc-import name="Avatar" color="teal" state="happy" dot="none" ring="#070707" size="80" hint-size="80px,80px"></dc-import>
<span style="font-size: 20px; margin-top: 6px">客服小蝦 已加入</span>
<span style="font-size: 14px; color: #FCFCFC99">電腦在背景準備中，第一個工作進來就會開機。</span>
</div>
</sc-if>`;

const body = `${SAFE_TOP}
${header(backBtn('L01-PhoneList.dc.html'), '新增 Bot')}
${tagRow('2 個畫面 10 個欄位 → 1 個欄位')}
<div style="flex: 1; min-height: 0; overflow-y: auto; padding: 0 16px 12px; display: flex; flex-direction: column; gap: 14px">
<sc-if value="{{notDone}}" hint-placeholder-val="{{ true }}">
<label style="display: flex; flex-direction: column; gap: 8px">
<span style="font-size: 20px; line-height: 1.35">一句話說你要它做什麼</span>
<textarea rows="3" placeholder="例：每天整理新聞給我" style="min-height: 92px; box-sizing: border-box; padding: 12px 14px; border: 1px solid {{inputLine}}; border-radius: 14px; background: #191919; color: #FCFCFC; font: inherit; font-size: 15px; line-height: 1.55; resize: none; outline: none">每天幫我回覆蝦皮買家的問題，不確定的先問我</textarea>
</label>
<sc-if value="{{isTyping}}" hint-placeholder-val="{{ false }}">
<button type="button" onClick="{{suggest}}" style="height: 48px; border: 0; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; display: flex; align-items: center; justify-content: center; gap: 8px; cursor: pointer">${I('sparkles', 18)}幫我想好其他的</button>
<div style="display: flex; flex-direction: column; gap: 6px">
<span style="font-size: 13px; color: #FCFCFC90">別人常這樣寫</span>
<button type="button" style="min-height: 44px; padding: 0 14px; border: 1px solid #333333; border-radius: 12px; background: transparent; color: #FCFCFC99; font-size: 14px; text-align: left; cursor: pointer">每週一整理上週的業績給我</button>
<button type="button" style="min-height: 44px; padding: 0 14px; border: 1px solid #333333; border-radius: 12px; background: transparent; color: #FCFCFC99; font-size: 14px; text-align: left; cursor: pointer">幫我看 Gmail，重要的信才通知我</button>
</div>
</sc-if>
</sc-if>
${card}
${done}
</div>
<div style="flex: none; padding: 8px 16px 0; display: flex; flex-direction: column; gap: 6px">
<sc-if value="{{showCard}}" hint-placeholder-val="{{ true }}">
<button type="button" onClick="{{create}}" style="height: 48px; border: 0; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; cursor: pointer">建立</button>
<div style="display: flex; align-items: center; justify-content: space-between">
<button type="button" onClick="{{retry}}" style="height: 44px; padding: 0 8px; border: 0; background: transparent; color: #FCFCFC99; font-size: 14px; display: flex; align-items: center; gap: 6px; cursor: pointer">${I('sparkles', 16)}重新建議</button>
<span style="font-size: 12.5px; color: #FCFCFC90">今天還能建立 2 個</span>
</div>
</sc-if>
<sc-if value="{{isDone}}" hint-placeholder-val="{{ false }}">
<a href="L03-PhoneChat.dc.html" style="height: 48px; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; display: flex; align-items: center; justify-content: center">跟小蝦說話</a>
<button type="button" onClick="{{retry}}" style="height: 44px; border: 0; background: transparent; color: #FCFCFC99; font-size: 14px; cursor: pointer">再建一個</button>
</sc-if>
</div>
<div aria-hidden="true" style="height: 34px; flex: none"></div>`;

const script = `
class Component extends DCLogic {
renderVals() {
const v = (this.state && this.state.v) || 'card';
return {
isTyping: v === 'typing', showCard: v === 'card', isDone: v === 'done', notDone: v !== 'done',
inputLine: v === 'typing' ? '#459FFE' : '#333333',
suggest: () => this.setState({ v: 'card' }),
create: () => this.setState({ v: 'done' }),
retry: () => this.setState({ v: 'typing' })
};
}
}`;

console.log('L12', page({ file: 'L12-PhoneNewBot.dc.html', title: '手機：新增 Bot', body, script }));
