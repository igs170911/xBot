import { I, simpTag, tagRow, page, header, backBtn, SAFE_TOP, SAFE_BOTTOM } from './lib.mjs';

// a drawn QR: deterministic modules on a 25 grid
function qr(px) {
  const n = 25, c = px / n; let r = '';
  const finder = (x, y) => (x < 7 && y < 7) || (x >= n - 7 && y < 7) || (x < 7 && y >= n - 7);
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) {
    if (finder(x, y)) continue;
    const v = ((x * 7 + y * 13 + x * y * 3) % 5) < 2;
    if (v) r += `<rect x="${(x * c).toFixed(2)}" y="${(y * c).toFixed(2)}" width="${c.toFixed(2)}" height="${c.toFixed(2)}"></rect>`;
  }
  const f = (x, y) => `<rect x="${x * c + c / 2}" y="${y * c + c / 2}" width="${6 * c}" height="${6 * c}" fill="none" stroke="#111111" stroke-width="${c}"></rect><rect x="${(x + 2) * c}" y="${(y + 2) * c}" width="${3 * c}" height="${3 * c}"></rect>`;
  return `<svg width="${px}" height="${px}" viewBox="0 0 ${px} ${px}" aria-hidden="true" style="display: block"><g fill="#111111">${r}${f(0, 0)}${f(n - 7, 0)}${f(0, n - 7)}</g></svg>`;
}

const corner = (pos) => {
  const m = { tl: 'top: 0; left: 0; border-width: 4px 0 0 4px; border-radius: 18px 0 0 0', tr: 'top: 0; right: 0; border-width: 4px 4px 0 0; border-radius: 0 18px 0 0', bl: 'bottom: 0; left: 0; border-width: 0 0 4px 4px; border-radius: 0 0 0 18px', br: 'bottom: 0; right: 0; border-width: 0 4px 4px 0; border-radius: 0 0 18px 0' }[pos];
  return `<span style="position: absolute; ${m}; width: 44px; height: 44px; border-style: solid; border-color: #FCFCFC; box-sizing: border-box"></span>`;
};

const scan = `
<sc-if value="{{isScan}}" hint-placeholder-val="{{ true }}">
<div style="flex: 1; min-height: 0; padding: 0 16px; display: flex; flex-direction: column; gap: 14px">
<p style="margin: 0; font-size: 14.5px; color: #FCFCFC99">在電腦上打開「設定 › 電腦與裝置 › 手機」，把畫面上的 QR 放進框裡。</p>
<button type="button" onClick="{{doScan}}" aria-label="相機畫面：對準電腦上的 QR" style="position: relative; height: 330px; flex: none; border: 0; padding: 0; border-radius: 22px; overflow: hidden; cursor: pointer; background: radial-gradient(120% 90% at 50% 40%, #2A2D33 0%, #15171B 55%, #0B0C0E 100%)">
<span aria-hidden="true" style="position: absolute; left: 50%; top: 50%; width: 260px; height: 190px; transform: translate(-50%, -54%) rotate(-3deg); border-radius: 10px; background: #1D2024; box-shadow: 0 8px 30px rgba(0,0,0,.6); display: flex; align-items: center; justify-content: center">
<span style="width: 136px; height: 136px; border-radius: 6px; background: #E9EAEC; display: flex; align-items: center; justify-content: center; filter: blur(.4px)">${qr(116)}</span>
</span>
<span aria-hidden="true" style="position: absolute; left: 50%; top: 50%; width: 200px; height: 200px; transform: translate(-50%, -50%)">
${corner('tl')}${corner('tr')}${corner('bl')}${corner('br')}
<span style="position: absolute; left: 12px; right: 12px; top: 58%; height: 2px; border-radius: 2px; background: #1084FE; box-shadow: 0 0 12px #1084FE"></span>
</span>
<span aria-hidden="true" style="position: absolute; left: 0; right: 0; bottom: 14px; display: flex; justify-content: center"><span style="height: 30px; padding: 0 12px; border-radius: 999px; background: rgba(0,0,0,.55); color: #FCFCFC; font-size: 13px; line-height: 30px">對準 QR，會自動掃描</span></span>
</button>
<div style="display: flex; align-items: flex-start; gap: 10px; padding: 12px 14px; border-radius: 14px; background: #262626">
<span style="color: #38D591; display: flex; padding-top: 2px">${I('shield-check', 18)}</span>
<span style="flex: 1; font-size: 13.5px; color: #FCFCFC99">手機先裝 Tailscale，登入和電腦同一個帳號。走正式 HTTPS 憑證，不用另外安裝任何憑證。</span>
</div>
<div style="margin-top: auto; display: flex; flex-direction: column; gap: 4px">
<button type="button" onClick="{{toWifi}}" style="height: 44px; border: 0; background: transparent; color: #7FC0FF; font-size: 14.5px; display: flex; align-items: center; justify-content: center; gap: 6px; cursor: pointer">${I('wifi', 18)}沒有 Tailscale？改用同一個 Wi-Fi</button>
</div>
</div>
</sc-if>`;

const done = `
<sc-if value="{{isDone}}" hint-placeholder-val="{{ false }}">
<div style="flex: 1; min-height: 0; padding: 8px 16px 0; display: flex; flex-direction: column; gap: 18px">
<div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 12px 0 4px">
<span style="width: 64px; height: 64px; border-radius: 999px; background: rgba(56,213,145,.16); color: #38D591; display: flex; align-items: center; justify-content: center">${I('check', 32, 'currentColor', 2)}</span>
<h2 style="margin: 4px 0 0; font-size: 22px; font-weight: 400">已連上</h2>
<span style="font-family: 'JetBrains Mono', monospace; font-size: 14px; color: #FCFCFC99">xbot.tail3a9e.ts.net</span>
</div>
<section aria-labelledby="can" style="display: flex; flex-direction: column; gap: 6px">
<h3 id="can" style="margin: 0; padding-left: 14px; font-size: 13px; font-weight: 400; color: #FCFCFC90">這支手機可以</h3>
<ul style="margin: 0; padding: 0 14px; list-style: none; background: #262626; border-radius: 14px">
<li style="min-height: 60px; display: flex; align-items: center; gap: 12px"><span style="color: #FCFCFC; display: flex">${I('message-circle', 20)}</span><span style="flex: 1; display: flex; flex-direction: column"><span style="font-size: 15px">聊天</span><span style="font-size: 13px; color: #FCFCFC99">跟每個 Bot 和群組說話</span></span></li>
<li style="min-height: 60px; display: flex; align-items: center; gap: 12px; border-top: 1px solid #333333"><span style="color: #FCFCFC; display: flex">${I('check-check', 20)}</span><span style="flex: 1; display: flex; flex-direction: column"><span style="font-size: 15px">核准</span><span style="font-size: 13px; color: #FCFCFC99">付款、上架前在手機上按「允許這次」</span></span></li>
<li style="min-height: 60px; display: flex; align-items: center; gap: 12px; border-top: 1px solid #333333"><span style="color: #FCFCFC; display: flex">${I('monitor', 20)}</span><span style="flex: 1; display: flex; flex-direction: column"><span style="font-size: 15px">看電腦</span><span style="font-size: 13px; color: #FCFCFC99">看 Bot 的電腦畫面，需要時接手</span></span></li>
</ul>
</section>
<p style="margin: 0; padding: 0 4px; font-size: 13px; color: #FCFCFC90">這支手機會記住登入 30 天，有在用就自動延長。不用時可以在設定裡移除。</p>
<div style="margin-top: auto; display: flex; flex-direction: column; gap: 8px">
<a href="L02-PhoneInbox.dc.html" style="height: 48px; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; display: flex; align-items: center; justify-content: center">開始使用</a>
<button type="button" onClick="{{reset}}" style="height: 44px; border: 0; background: transparent; color: #FCFCFC99; font-size: 14.5px; cursor: pointer">重新掃描</button>
</div>
</div>
</sc-if>`;

const wifi = `
<sc-if value="{{isWifi}}" hint-placeholder-val="{{ false }}">
<div style="flex: 1; min-height: 0; padding: 0 16px; display: flex; flex-direction: column; gap: 14px">
<div style="display: flex; align-items: center; gap: 8px; padding: 10px 14px; border-radius: 14px; background: rgba(255,152,0,.12); color: #FFB547; font-size: 13.5px">${I('wifi', 18)}<span style="flex: 1">只在同一個 Wi-Fi 有效，出門就連不上</span></div>
<ol style="margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 10px">
<li style="display: flex; gap: 12px; padding: 14px; background: #262626; border-radius: 14px"><span style="width: 26px; height: 26px; flex: none; border-radius: 999px; background: #2F2F2F; font-size: 13.5px; line-height: 26px; text-align: center">1</span><span style="flex: 1; display: flex; flex-direction: column; gap: 2px"><span style="font-size: 15px">信任這台電腦的憑證</span><span style="font-size: 13px; color: #FCFCFC99">掃電腦上的第一個 QR，照手機的提示安裝。每支手機只要做一次。</span></span></li>
<li style="display: flex; gap: 12px; padding: 14px; background: #262626; border-radius: 14px"><span style="width: 26px; height: 26px; flex: none; border-radius: 999px; background: #2F2F2F; font-size: 13.5px; line-height: 26px; text-align: center">2</span><span style="flex: 1; display: flex; flex-direction: column; gap: 2px"><span style="font-size: 15px">掃登入 QR</span><span style="font-size: 13px; color: #FCFCFC99">掃第二個 QR 就會登入。</span></span></li>
</ol>
<div style="margin-top: auto; display: flex; flex-direction: column; gap: 8px">
<button type="button" style="height: 48px; border: 0; border-radius: 999px; background: #FCFCFC; color: #070707; font-size: 15.5px; cursor: pointer">開始掃第一個 QR</button>
<button type="button" onClick="{{reset}}" style="height: 44px; border: 0; background: transparent; color: #7FC0FF; font-size: 14.5px; cursor: pointer">改用 Tailscale（建議）</button>
</div>
</div>
</sc-if>`;

const body = `${SAFE_TOP}
${header(backBtn('L06-PhoneSettings.dc.html'), '{{title}}')}
${tagRow('2 個 QR＋裝憑證 → 1 個 QR')}
${scan}
${done}
${wifi}
<div aria-hidden="true" style="height: 46px; flex: none"></div>`;

const script = `
class Component extends DCLogic {
renderVals() {
const v = (this.state && this.state.v) || 'scan';
return {
isScan: v === 'scan', isDone: v === 'done', isWifi: v === 'wifi',
title: v === 'wifi' ? '用同一個 Wi-Fi 連' : (v === 'done' ? '手機已連上' : '連接這支手機'),
doScan: () => this.setState({ v: 'done' }),
toWifi: () => this.setState({ v: 'wifi' }),
reset: () => this.setState({ v: 'scan' })
};
}
}`;

console.log('L09', page({ file: 'L09-PhonePair.dc.html', title: '手機：連接這支手機', body, script }));
