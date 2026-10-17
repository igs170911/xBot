import fs from 'fs';
const DIR = '/home/daniel/Projects/LazyBoyHermes/web/node_modules/lucide-react/dist/esm/icons/';
const names = process.argv.slice(2);
const toD = ([tag, a]) => {
  const n = k => +a[k];
  if (tag === 'path') return a.d;
  if (tag === 'line') return `M${a.x1} ${a.y1}L${a.x2} ${a.y2}`;
  if (tag === 'polyline' || tag === 'polygon') { const p = a.points.trim().split(/[\s,]+/); let d = `M${p[0]} ${p[1]}`; for (let i = 2; i < p.length; i += 2) d += `L${p[i]} ${p[i+1]}`; return tag === 'polygon' ? d + 'Z' : d; }
  if (tag === 'circle') { const cx = n('cx'), cy = n('cy'), r = n('r'); return `M${cx - r} ${cy}a${r} ${r} 0 1 0 ${2*r} 0a${r} ${r} 0 1 0 ${-2*r} 0`; }
  if (tag === 'ellipse') { const cx = n('cx'), cy = n('cy'), rx = n('rx'), ry = n('ry'); return `M${cx - rx} ${cy}a${rx} ${ry} 0 1 0 ${2*rx} 0a${rx} ${ry} 0 1 0 ${-2*rx} 0`; }
  if (tag === 'rect') { const x = n('x'), y = n('y'), w = n('width'), h = n('height'); const r = Math.min(+(a.rx || a.ry || 0), w / 2, h / 2);
    if (!r) return `M${x} ${y}h${w}v${h}h${-w}Z`;
    return `M${x + r} ${y}h${w - 2*r}a${r} ${r} 0 0 1 ${r} ${r}v${h - 2*r}a${r} ${r} 0 0 1 ${-r} ${r}h${-(w - 2*r)}a${r} ${r} 0 0 1 ${-r} ${-r}v${-(h - 2*r)}a${r} ${r} 0 0 1 ${r} ${-r}Z`; }
  throw new Error('tag ' + tag);
};
const out = {};
for (const nm of names) {
  const src = fs.readFileSync(DIR + nm + '.mjs', 'utf8');
  const m = src.match(/node:\s*(\[[\s\S]*?\])\s*(,\s*aliases[^\n]*)?\n\};/);
  const node = eval(m[1]);
  out[nm] = node.map(toD);
}
console.log(JSON.stringify(out));
