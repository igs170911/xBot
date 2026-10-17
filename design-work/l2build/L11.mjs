import { I, page, SAFE_TOP } from './lib.mjs';

const bg = `
<div aria-hidden="true" style="position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center">
<div style="height: 47px; flex: none"></div>
<div style="align-self: stretch; height: 56px; padding: 0 12px; display: flex; align-items: center; gap: 10px">
<span style="width: 44px; height: 44px; box-sizing: border-box; border-radius: 999px; background: #191919; border: 1px solid #333333; display: flex; align-items: center; justify-content: center">${I('chevron-left', 22)}</span><span style="font-size: 17px">Bot 資料</span></div>
<div style="margin-top: 30px"><dc-import name="Avatar" color="blue" state="working" dot="none" ring="#070707" size="88" hint-size="88px,88px"></dc-import></div>
<span style="margin-top: 8px; font-size: 22px">小秘</span>
</div>
<div aria-hidden="true" style="position: absolute; inset: 0; background: rgba(0,0,0,.62)"></div>`;

const sheet = `
<section role="dialog" aria-modal="true" aria-labelledby="av-title" style="position: absolute; left: 0; right: 0; bottom: 0; background: #111111; border-radius: 20px 20px 0 0; border-top: 1px solid #262626; padding: 0 16px 34px; display: flex; flex-direction: column; gap: 14px">
<div aria-hidden="true" style="height: 14px; display: flex; align-items: flex-end; justify-content: center"><span style="width: 36px; height: 5px; border-radius: 9px; background: #3D3D3D"></span></div>
<div style="display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; margin-top: -6px">
<a href="L10-PhoneBotProfile.dc.html" style="justify-self: start; height: 44px; padding: 0 4px; display: flex; align-items: center; color: #FCFCFC99; font-size: 15px">取消</a>
<h2 id="av-title" style="margin: 0; font-size: 17px; font-weight: 400">頭像</h2>
<a href="L10-PhoneBotProfile.dc.html" style="justify-self: end; height: 44px; padding: 0 18px; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 14.5px; display: flex; align-items: center; ">完成</a>
</div>

<div style="display: flex; align-items: center; justify-content: center; height: 112px">
<sc-if value="{{isMascot}}" hint-placeholder-val="{{ true }}">
<dc-import name="Avatar" color="{{color}}" state="{{expr}}" dot="none" ring="#111111" size="104" hint-size="104px,104px"></dc-import>
</sc-if>
<sc-if value="{{isFramed}}" hint-placeholder-val="{{ false }}">
<span style="width: 108px; height: 108px; border-radius: {{radius}}; background: {{tint}}; display: flex; align-items: center; justify-content: center; overflow: hidden"><dc-import name="Avatar" color="{{color}}" state="{{expr}}" dot="none" ring="#111111" size="76" hint-size="76px,76px"></dc-import></span>
</sc-if>
</div>

<div role="radiogroup" aria-label="形狀" style="display: flex; padding: 3px; border-radius: 10px; background: #191919; gap: 2px">
<sc-for list="{{shapes}}" as="s" hint-placeholder-count="4">
<button type="button" role="radio" aria-checked="{{s.on}}" onClick="{{s.pick}}" style="flex: 1; height: 44px; border: 0; border-radius: 8px; background: {{s.bg}}; color: {{s.ink}}; font-size: 14px; cursor: pointer">{{s.label}}</button>
</sc-for>
</div>

<div style="display: flex; flex-direction: column; gap: 6px">
<h3 style="margin: 0; padding-left: 2px; font-size: 13px; font-weight: 400; color: #FCFCFC90">表情</h3>
<div role="radiogroup" aria-label="表情" style="display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px">
<sc-for list="{{exprs}}" as="x" hint-placeholder-count="8">
<button type="button" role="radio" aria-checked="{{x.on}}" aria-label="{{x.label}}" onClick="{{x.pick}}" style="height: 66px; border: 1px solid {{x.line}}; border-radius: 12px; background: {{x.bg}}; color: #FCFCFC99; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 2px; cursor: pointer; padding: 0">
<dc-import name="Avatar" color="{{color}}" state="{{x.state}}" dot="none" ring="#191919" size="34" hint-size="34px,34px"></dc-import>
<span style="font-size: 11.5px; line-height: 1.2">{{x.label}}</span>
</button>
</sc-for>
</div>
</div>

<div style="display: flex; flex-direction: column; gap: 6px">
<h3 style="margin: 0; padding-left: 2px; font-size: 13px; font-weight: 400; color: #FCFCFC90">顏色</h3>
<div role="radiogroup" aria-label="顏色" style="display: grid; grid-template-columns: repeat(5, minmax(0, 1fr))">
<sc-for list="{{colors}}" as="c" hint-placeholder-count="10">
<button type="button" role="radio" aria-checked="{{c.on}}" aria-label="{{c.label}}" onClick="{{c.pick}}" style="height: 44px; border: 0; padding: 0; background: transparent; display: flex; align-items: center; justify-content: center; cursor: pointer">
<span style="width: 28px; height: 28px; box-sizing: border-box; border-radius: 999px; background: {{c.hex}}; box-shadow: {{c.ring}}; display: flex; align-items: center; justify-content: center; color: #FFFFFF"><sc-if value="{{c.on}}" hint-placeholder-val="{{ false }}">${I('check', 15, 'currentColor', 2.5)}</sc-if></span>
</button>
</sc-for>
</div>
</div>

<div style="background: #262626; border-radius: 14px">
<a href="G04-AvatarUpload.dc.html" style="height: 48px; display: flex; align-items: center; gap: 12px; padding: 0 14px; color: #7FC0FF; font-size: 15px">${I('image-plus', 20)}<span style="flex: 1">上傳圖片</span><span style="font-size: 12.5px; color: #FCFCFC90">PNG、JPG，10 MB 內</span></a>
<div style="height: 1px; background: #333333; margin-left: 46px"></div>
<a href="G04-AvatarUpload.dc.html" style="height: 48px; display: flex; align-items: center; gap: 12px; padding: 0 14px; color: #FCFCFC; font-size: 15px"><span style="color: #7FC0FF; display: flex">${I('wand-sparkles', 20)}</span><span style="flex: 1">用 AI 產生</span><span style="color: #FCFCFC90; display: flex">${I('chevron-right', 18)}</span></a>
</div>
</section>`;

const body = `${bg}${sheet}`;

const script = `
class Component extends DCLogic {
renderVals() {
const st = this.state || {};
const color = st.color || 'blue';
const expr = st.expr || 'happy';
const shape = st.shape || '吉祥物';
const HEX = { green: '#009957', blue: '#377FE6', red: '#D94B52', orange: '#E78531', purple: '#8057C8', cyan: '#0EA5C6', pink: '#D84F8B', yellow: '#D8A729', teal: '#01A492', coral: '#E5634E' };
const NAMES = { green: '綠', blue: '藍', red: '紅', orange: '橘', purple: '紫', cyan: '青', pink: '粉紅', yellow: '黃', teal: '藍綠', coral: '珊瑚' };
const R = { '圓': '999px', '圓角': '30px', '方': '8px' };
const exprs = [['idle', '平常'], ['happy', '開心'], ['working', '專心'], ['thinking', '思考'], ['curious', '好奇'], ['surprised', '驚訝'], ['proud', '得意'], ['sleeping', '想睡']].map(([state, label]) => ({
state, label, on: state === expr, bg: state === expr ? '#2F2F2F' : '#191919', line: state === expr ? '#459FFE' : '#191919',
pick: () => this.setState({ expr: state })
}));
const colors = Object.keys(HEX).map((k) => ({
label: NAMES[k], hex: HEX[k], on: k === color,
ring: k === color ? '0 0 0 2px #111111, 0 0 0 4px #FCFCFC' : 'none',
pick: () => this.setState({ color: k })
}));
const shapes = ['吉祥物', '圓', '圓角', '方'].map((label) => ({ label, on: label === shape, bg: label === shape ? '#2F2F2F' : 'transparent', ink: label === shape ? '#FCFCFC' : '#FCFCFC99', pick: () => this.setState({ shape: label }) }));
return {
color, expr, shapes, exprs, colors,
isMascot: shape === '吉祥物', isFramed: shape !== '吉祥物',
radius: R[shape] || '999px', tint: '#2F2F2F'
};
}
}`;

console.log('L11', page({ file: 'L11-PhoneAvatarSheet.dc.html', title: '手機：換頭像', body, script }));
