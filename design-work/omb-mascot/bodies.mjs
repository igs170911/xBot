import fs from 'fs';
const src = fs.readFileSync('/home/daniel/Projects/OpenMausBot/shared/mascot-bodies.ts', 'utf8').replace(/^import.*$/m, '').replace(/export const mascotBodySchema.*$/m, '').replace(/export function botMascotBody[\s\S]*$/, '').replace(/export (interface|type)[\s\S]*?\n}\n/g, '').replace(/export type .*$/gm, '').replace(/ as const/g, '').replace(/: Record<MascotBodyId, MascotBody>/, '').replace(/: MascotBodyId/, '').replace(/export /g, '');
const B = eval(src + '\n;MASCOT_BODIES');
const out = {};
for (const [id, b] of Object.entries(B)) out[id] = { fit: b.fit, d: b.body.match(/ d="([^"]+)"/)[1], clip: b.clip.match(/ d="([^"]+)"/)[1], a: [b.anchor.x, b.anchor.y, b.anchor.scale] };
fs.writeFileSync(process.argv[2], JSON.stringify(out));
