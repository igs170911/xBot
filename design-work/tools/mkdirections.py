s=open('S1-Gummy.dc.html',encoding='utf-8').read()
a=s.index("const INK = '#1B1B22';"); b=s.index("const ICONS = ")
head, mid, tail = s[:a], s[a:b], s[b:]

common_helpers = r"""const INK = '#1B1B22';
const mix = (hex, to, t) => { const n = parseInt(hex.slice(1), 16), m = parseInt(to.slice(1), 16); const c = (k) => Math.round(((n >> k) & 255) * (1 - t) + ((m >> k) & 255) * t); return '#' + [16, 8, 0].map((k) => c(k).toString(16).padStart(2, '0')).join(''); };
const ol = (d, f, w) => ({ d, f, fo: 1, s: INK, w: w || 2 });
const RR = (x, y, w, h, r) => `M${x + r} ${y}h${w - 2 * r}a${r} ${r} 0 0 1 ${r} ${r}v${h - 2 * r}a${r} ${r} 0 0 1 ${-r} ${r}h${-(w - 2 * r)}a${r} ${r} 0 0 1 ${-r} ${-r}v${-(h - 2 * r)}a${r} ${r} 0 0 1 ${r} ${-r}Z`;
"""

mono = common_helpers + r"""const BODY = RR(8, 10, 32, 30, 13);
const face = (mood) => {
const dots = (dx, dy) => [fill(C(19 + dx, 25 + dy, 2.3), INK), fill(C(29 + dx, 25 + dy, 2.3), INK)];
if (mood === 'working') return [...dots(1.4, -1.8), line('M22 32.5h4', INK, 2)];
if (mood === 'waiting') return [...dots(0, 1.8), line('M21.5 33q2.5 -1.6 5 0', INK, 2)];
if (mood === 'error') return [line('M16.8 22.8l4.4 4.4M21.2 22.8l-4.4 4.4M26.8 22.8l4.4 4.4M31.2 22.8l-4.4 4.4', INK, 2), line('M21 33.5q3 -2.2 6 0', INK, 2)];
if (mood === 'done') return [line('M16.4 26q2.6 -4.2 5.2 0M26.4 26q2.6 -4.2 5.2 0', INK, 2), line('M20.5 31q3.5 3.6 7 0', INK, 2)];
if (mood === 'sleep') return [line('M16.4 25.5h5.2M26.4 25.5h5.2', INK, 2), line('M22.5 32.5h3', INK, 2)];
return [...dots(0, 0), line('M21 31.5q3 2.6 6 0', INK, 2)];
};
const badge = (kind, big) => {
const map = { working: '#FFFFFF', waiting: '#FFC53D', idle: '#E4E1E6', error: '#FF6B6B' };
if (!map[kind]) return [];
const r = big ? 9 : 6.6, w = big ? 3 : 2;
const o = [ol(C(40, 40, r), map[kind], w)];
if (big) return o;
if (kind === 'working') o.push(fill(C(37.4, 40, 1), INK), fill(C(40, 40, 1), INK), fill(C(42.6, 40, 1), INK));
if (kind === 'waiting') o.push(line('M40 37v3.2', INK, 2), fill(C(40, 43, 1), INK));
if (kind === 'error') o.push(line('M38 38l4 4M42 38l-4 4', INK, 2));
if (kind === 'idle') o.push(line('M38 40h4', INK, 2));
return o;
};
const bot = (color, mood, st) => [line('M24 10V5.5', INK, 2), ol(C(24, 4, 2.2), mix(color, '#FFFFFF', 0.45)), ol(BODY, mix(color, '#FFFFFF', 0.45)), ...face(mood), ...badge(st)];
const small = (color, st) => [ol(BODY, mix(color, '#FFFFFF', 0.45), 3), fill(C(19, 25, 3.2), INK), fill(C(29, 25, 3.2), INK), ...badge(st, true)];
const MC = '#FFF1D6';
const mascot = (pose) => {
const o = [fill(E(32, 61, 17, 2.4), '#000000', 0.08), line('M32 13V7', INK, 2.4), ol(C(32, 5, 2.8), '#FFC53D', 2.4)];
if (pose === 'happy') o.push(line('M12 33l-6 -9M52 33l6 -9', INK, 2.4));
else if (pose === 'work') o.push(line('M12 40l-5 4M52 36l6 -6', INK, 2.4));
else o.push(line('M12 40l-5 4M52 40l5 4', INK, 2.4));
o.push(line('M24 57v4M40 57v4', INK, 2.4), ol(RR(10, 13, 44, 44, 18), MC, 2.4));
if (pose === 'sleep') o.push(line('M20 35h8M36 35h8', INK, 2.4), line('M29.5 44h5', INK, 2.4), line('M46 3h7l-7 8h7', INK, 2));
else if (pose === 'happy') o.push(line('M19 36q5 -7 10 0M35 36q5 -7 10 0', INK, 2.4), line('M26 42q6 6 12 0', INK, 2.4));
else if (pose === 'work') o.push(fill(C(26, 32, 2.8), INK), fill(C(41, 32, 2.8), INK), line('M29 43h6', INK, 2.4), ol('M53 22l1.4 3.6 3.6 1.4-3.6 1.4-1.4 3.6-1.4-3.6-3.6-1.4 3.6-1.4Z', '#FFC53D', 2));
else o.push(fill(C(24, 34, 2.8), INK), fill(C(40, 34, 2.8), INK), line('M27 42q5 4 10 0', INK, 2.4), fill(E(18, 41, 3, 1.8), '#FF8FA8', 0.8), fill(E(46, 41, 3, 1.8), '#FF8FA8', 0.8));
return o;
};
"""

tile = common_helpers + r"""const BODY = RR(6, 6, 36, 36, 11);
const W = '#FFFFFF';
const face = (mood) => {
const pills = (dx, dy, h) => [fill(RR(16.5 + dx, 17 + dy, 5, h || 10, 2.5), W), fill(RR(26.5 + dx, 17 + dy, 5, h || 10, 2.5), W)];
if (mood === 'working') return pills(1.5, -2.5);
if (mood === 'waiting') return pills(0, 3.5, 6.5);
if (mood === 'error') return [line('M16 19l6 6M22 19l-6 6M26 19l6 6M32 19l-6 6', W, 3)];
if (mood === 'done') return [line('M15.5 24q3.5 -6 7 0M25.5 24q3.5 -6 7 0', W, 3.2), line('M20 32q4 3 8 0', W, 2.8)];
if (mood === 'sleep') return [line('M16 23h7M25 23h7', W, 3), line('M48 2h6l-6 7h6', INK, 2.4)];
return [...pills(0, 0), line('M21 33h6', W, 2.8)];
};
const badge = (kind, big) => {
const map = { working: '#18181B', waiting: '#F59E0B', idle: '#A8A3AD', error: '#E5484D' };
if (!map[kind]) return [];
const s = big ? 22 : 15, x = 48 - s + (big ? 3 : 2);
const o = [{ d: RR(x, x, s, s, big ? 6 : 4.5), f: map[kind], fo: 1, s: W, w: big ? 4 : 2.6 }];
if (big) return o;
const c = x + s / 2;
if (kind === 'working') o.push(fill(C(c - 3, c, 1.15), W), fill(C(c, c, 1.15), W), fill(C(c + 3, c, 1.15), W));
if (kind === 'waiting') o.push(line(`M${c} ${c - 3.6}v3.8`, W, 2.2), fill(C(c, c + 3.2, 1.2), W));
if (kind === 'error') o.push(line(`M${c - 2.6} ${c - 2.6}l5.2 5.2M${c + 2.6} ${c - 2.6}l-5.2 5.2`, W, 2.2));
if (kind === 'idle') o.push(line(`M${c - 2.8} ${c}h5.6`, W, 2.2));
return o;
};
const bot = (color, mood, st) => [fill(RR(6, 9, 36, 36, 11), mix(color, '#000000', 0.28)), fill(RR(21.5, 1.5, 5, 7, 2), mix(color, '#000000', 0.28)), fill(BODY, color), ...face(mood), ...badge(st)];
const small = (color, st) => [fill(BODY, color), fill(RR(15.5, 15, 6.5, 14, 3.2), W), fill(RR(26, 15, 6.5, 14, 3.2), W), ...badge(st, true)];
const T = '#18181B';
const mascot = (pose) => {
const o = [fill(E(32, 61, 17, 2.4), '#000000', 0.10), fill(RR(20, 54, 7, 7, 2.5), T), fill(RR(37, 54, 7, 7, 2.5), T)];
if (pose === 'happy') o.push(fill(RR(3, 18, 8, 14, 4), T), fill(RR(53, 18, 8, 14, 4), T));
else if (pose === 'work') o.push(fill(RR(3, 34, 8, 14, 4), T), fill(RR(53, 24, 8, 14, 4), T));
else o.push(fill(RR(3, 34, 8, 14, 4), T), fill(RR(53, 34, 8, 14, 4), T));
o.push(fill(RR(10, 15, 44, 44, 13), '#3A3A44'), fill(RR(29, 4, 6, 9, 2.5), T), fill(RR(10, 11, 44, 44, 13), T));
if (pose === 'sleep') o.push(line('M20 33h9M35 33h9', W, 3.4), line('M47 1h7l-7 8h7', '#7C5CFF', 2.6));
else if (pose === 'happy') o.push(line('M19 35q5.5 -7.5 11 0M34 35q5.5 -7.5 11 0', W, 3.6), line('M27 43q5 4 10 0', W, 3.2));
else if (pose === 'work') o.push(fill(RR(23, 23, 6.5, 13, 3.2), W), fill(RR(37, 23, 6.5, 13, 3.2), W), line('M29 44h6', W, 3), fill('M57 4l1.6 4.2L63 10l-4.4 1.6L57 16l-1.6-4.4L51 10l4.4-1.8Z', '#FFC53D'));
else o.push(fill(RR(21.5, 25, 6.5, 13, 3.2), W), fill(RR(36, 25, 6.5, 13, 3.2), W), line('M28 44q4 3 8 0', W, 3), fill(RR(14, 41, 6, 3.5, 1.75), '#FF8FA8'), fill(RR(44, 41, 6, 3.5, 1.75), '#FF8FA8'));
return o;
};
"""

def make(fname, renderer, edits, iconfill_none=True, stroke='2'):
    t = head + renderer + tail
    for a_, b_ in edits: 
        assert a_ in t, a_[:60]
        t = t.replace(a_, b_)
    if iconfill_none:
        t = t.replace("paths: i.d.map((d) => ({ d, f: closed(d) ? 'currentColor' : 'none', fo: closed(d) ? 0.18 : 1 }))", "paths: i.d.map((d) => ({ d, f: 'none', fo: 1 }))")
    t = t.replace('stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path></sc-for></svg>', f'stroke="currentColor" stroke-width="{stroke}" stroke-linecap="round" stroke-linejoin="round"></path></sc-for></svg>')
    open(fname, 'w', encoding='utf-8').write(t)

make('S2-Monoline.dc.html', mono, [
 ('<title>風格方向 A 軟糖</title>', '<title>風格方向 B 單線</title>'),
 ('background: #FBF7F2; color: #1B1B22', 'background: #F6F6F3; color: #1B1B22'),
 ('border-bottom: 2px solid #EFE6DA', 'border-bottom: 2px solid #1B1B22'),
 ('方向 A · 推薦', '方向 B'),
 ('軟糖：圓潤、實心、好捏的小東西', '單線：一支筆畫完所有東西'),
 ('沒有外框，全部用實心圓角形狀；圖示是同樣圓頭圓角的線條，封閉的部分填一層淡色（雙色調）。氣質：可愛、親切、像手機上的貼圖。適合：一人公司、非技術使用者、第一次用 AI 的人。', '吉祥物、頭像、圖示、卡片都用同一支 2 px 黑色圓頭線，裡面填淡色。氣質：乾淨、知性、像手繪筆記本。適合：小團隊、常看文件與表格的人、喜歡工具感的使用者。'),
 ('線寬 2 · 圓頭</span>', '線寬 2 · 全站同一支筆</span>'),
 ('圓角 999 / 20</span>', '圓角 10 / 14</span>'),
 ('雙色調填色 18%</span>', '頭像淡色 45%</span>'),
 ('吉祥物「糖糖」· 4 種姿勢', '吉祥物「線線」· 4 種姿勢'),
 ('border-radius: 999px; background: #F1E7DA; padding: 4px 12px', 'border-radius: 999px; background: #FFFFFF; border: 1.5px solid #1B1B22; padding: 4px 12px'),

 ('color: #8A6A4A', 'color: #1B1B22'),
 ('background: #FFFFFF; border-radius: 24px; padding: 20px; box-shadow: 0 6px 24px rgba(120, 80, 40, 0.10)', 'background: #FFFFFF; border-radius: 14px; padding: 20px; border: 1.5px solid #1B1B22'),
 ('min-height: 40px; padding: 0 20px; border: 0; border-radius: 999px; background: #1B1B22', 'min-height: 40px; padding: 0 18px; border: 1.5px solid #1B1B22; border-radius: 10px; background: #1B1B22'),
 ('min-height: 40px; padding: 0 20px; border: 0; border-radius: 999px; background: #F1E7DA', 'min-height: 40px; padding: 0 18px; border: 1.5px solid #1B1B22; border-radius: 10px; background: #FFFFFF'),
 ('border-radius: 999px; background: #FFE7C2; color: #8A4B00; padding: 3px 10px', 'border-radius: 999px; background: #FFC53D; color: #1B1B22; border: 1.5px solid #1B1B22; padding: 2px 10px'),
], stroke='2')

make('S3-Tile.dc.html', tile, [
 ('<title>風格方向 A 軟糖</title>', '<title>風格方向 C 積木</title>'),
 ('background: #FBF7F2; color: #1B1B22', 'background: #F0EFEA; color: #1B1B22'),
 ('border-bottom: 2px solid #EFE6DA', 'border-bottom: 3px solid #1B1B22'),
 ('方向 A · 推薦', '方向 C'),
 ('軟糖：圓潤、實心、好捏的小東西', '積木：方方的、有厚度、按下去會回彈'),
 ('沒有外框，全部用實心圓角形狀；圖示是同樣圓頭圓角的線條，封閉的部分填一層淡色（雙色調）。氣質：可愛、親切、像手機上的貼圖。適合：一人公司、非技術使用者、第一次用 AI 的人。', '每個 Bot 是一顆有厚度的圓角方塊，白色膠囊眼睛；按鈕和卡片也有一樣的底部厚度，圖示用較粗的 2.5 線。氣質：活潑、有個性、像遊戲角色。適合：想要強烈品牌感、在小尺寸也要一眼認出的人。'),
 ('線寬 2 · 圓頭</span>', '線寬 2.5 · 粗圓頭</span>'),
 ('圓角 999 / 20</span>', '圓角 12 / 16 · 厚度 3</span>'),
 ('雙色調填色 18%</span>', '實心色塊＋白眼睛</span>'),
 ('吉祥物「糖糖」· 4 種姿勢', '吉祥物「方方」· 4 種姿勢'),
 ('color: #8A6A4A', 'color: #1B1B22'),
 ('background: #FFFFFF; border-radius: 24px; padding: 20px; box-shadow: 0 6px 24px rgba(120, 80, 40, 0.10)', 'background: #FFFFFF; border-radius: 16px; padding: 20px; border: 2px solid #1B1B22; box-shadow: 0 4px 0 #1B1B22'),
 ('min-height: 40px; padding: 0 20px; border: 0; border-radius: 999px; background: #1B1B22', 'min-height: 40px; padding: 0 18px; border: 2px solid #1B1B22; border-radius: 12px; box-shadow: 0 3px 0 #000000; background: #1B1B22'),
 ('min-height: 40px; padding: 0 20px; border: 0; border-radius: 999px; background: #F1E7DA', 'min-height: 40px; padding: 0 18px; border: 2px solid #1B1B22; border-radius: 12px; box-shadow: 0 3px 0 #1B1B22; background: #FFFFFF'),
 ('border-radius: 999px; background: #FFE7C2; color: #8A4B00; padding: 3px 10px', 'border-radius: 8px; background: #F59E0B; color: #1B1B22; padding: 3px 10px'),
], stroke='2.5')
print('done')
