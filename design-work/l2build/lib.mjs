import fs from 'fs';
const ICONS = JSON.parse(fs.readFileSync(new URL('./icons.json', import.meta.url), 'utf8'));
export const PROJ = '/tmp/claude-1000/-home-daniel-Projects/119fe167-bf85-4b35-9f2a-d88d25ba20e9/scratchpad/xbot-design/project';

export function I(name, size = 20, color = 'currentColor', sw = 1.75) {
  const ps = ICONS[name];
  if (!ps) throw new Error('icon ' + name);
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" style="flex: none; display: block">${ps.map(d => `<path d="${d}"></path>`).join('')}</svg>`;
}

export function simpTag(text) {
  return `<span style="height: 24px; display: inline-flex; align-items: center; gap: 4px; padding: 0 9px; border-radius: 999px; background: rgba(16,132,254,.2); color: #7FC0FF; font-size: 12px; white-space: nowrap; flex: none"><span style="display: flex">${I('sparkles', 13)}</span>簡化：${text}</span>`;
}

export const backBtn = (href, label = '返回') => `<a href="${href}" aria-label="${label}" style="width: 44px; height: 44px; flex: none; box-sizing: border-box; border-radius: 999px; background: #191919; border: 1px solid #333333; color: #FCFCFC; display: flex; align-items: center; justify-content: center">${I('chevron-left', 22)}</a>`;

// header: back · title (left) · right slot
export function header(back, title, right = '') {
  return `<header style="flex: none; height: 56px; padding: 0 12px; display: flex; align-items: center; gap: 10px">
${back}
<h1 style="margin: 0; flex: 1; min-width: 0; font-size: 17px; font-weight: 400; white-space: nowrap; overflow: hidden; text-overflow: ellipsis">${title}</h1>
${right}
</header>`;
}

// switch inside an sc-for item or bound by names
export function switchBtn(on, track, knob, flip, label) {
  return `<button type="button" role="switch" aria-checked="{{${on}}}" aria-label="${label}" onClick="{{${flip}}}" style="width: 52px; height: 44px; flex: none; border: 0; padding: 0; background: transparent; display: flex; align-items: center; justify-content: center; cursor: pointer"><span style="width: 40px; height: 24px; border-radius: 999px; background: {{${track}}}; position: relative; display: block"><span style="position: absolute; top: 2px; left: {{${knob}}}; width: 20px; height: 20px; border-radius: 999px; background: #FFFFFF"></span></span></button>`;
}

export const divider = `<div style="height: 1px; background: #333333; margin-left: 14px"></div>`;

export function page({ file, title, body, script, props = '{"$preview":{"width":390,"height":844}}', lh = '1.45' }) {
  const html = `<!doctype html>
<html lang="zh-Hant-TW">
<head>
<meta charset="utf-8">
<title>${title}</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Huninn&amp;family=JetBrains+Mono:wght@400;500&amp;display=swap">
<style>
body{margin:0;font-synthesis:none;background:#070707}
a{color:#7FC0FF;text-decoration:none}a:hover{color:#A8D4FF}
button{font:inherit}
textarea::placeholder,input::placeholder{color:#FCFCFC90}
</style>
</helmet>
<div style="width: 390px; height: 844px; position: relative; overflow: hidden; background: #070707; color: #FCFCFC; font-family: 'Huninn', 'PingFang TC', 'Noto Sans TC', sans-serif; font-size: 15px; line-height: ${lh}; display: flex; flex-direction: column">
${body}
</div>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='${props}'>
${script.trim()}
</script>
</body>
</html>
`;
  fs.writeFileSync(`${PROJ}/${file}`, html);
  return html.length;
}

export const SAFE_TOP = `<div aria-hidden="true" style="height: 47px; flex: none"></div>`;
export const SAFE_BOTTOM = `<div aria-hidden="true" style="height: 34px; flex: none"></div>`;
export const tagRow = (text) => `<div style="flex: none; display: flex; justify-content: flex-end; padding: 0 16px 10px; margin-top: -4px">${simpTag(text)}</div>`;
