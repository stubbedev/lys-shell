import fs from 'node:fs';
import path from 'node:path';
const base = './node_modules/@material/material-color-utilities/';
const { QuantizerCelebi } = await import(base + 'quantize/quantizer_celebi.js');
const { Score } = await import(base + 'score/score.js');
const { Hct } = await import(base + 'hct/hct.js');
const { DynamicScheme } = await import(base + 'dynamiccolor/dynamic_scheme.js');
const { MaterialDynamicColors } = await import(base + 'dynamiccolor/material_dynamic_colors.js');
const { Variant } = await import(base + 'dynamiccolor/variant.js');

const out = process.argv[2];
fs.mkdirSync(out, { recursive: true });

const MASK = (1n << 48n) - 1n;
let seed = 0n;
function setSeed(s) { seed = (BigInt(s) ^ 0x5DEECE66Dn) & MASK; }
function next(bits) {
  seed = (seed * 0x5DEECE66Dn + 0xBn) & MASK;
  return Number(seed >> BigInt(48 - bits));
}
Math.random = () => (next(26) * 134217728 + next(27)) / 9007199254740992;

function mulberry32(a) {
  return () => {
    a |= 0; a = a + 0x6D2B79F5 | 0;
    let t = Math.imul(a ^ a >>> 15, 1 | a);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}

const W = 64, H = 48;
const clamp = (v) => Math.max(0, Math.min(255, Math.round(v)));
const images = {
  sunset: (x, y, r) => [clamp(250 - y * 2 + r() * 20), clamp(120 + x - y * 2 + r() * 30), clamp(60 + y * 3 + r() * 20)],
  forest: (x, y, r) => {
    const n = r();
    if (n < 0.1) return [clamp(200 + r() * 40), clamp(180 + r() * 40), clamp(40 + r() * 30)];
    return [clamp(20 + r() * 40), clamp(90 + x + r() * 50), clamp(30 + r() * 30)];
  },
  sea: (x, y, r) => {
    const dx = x - 44, dy = y - 14;
    if (dx * dx + dy * dy < 64) return [clamp(240 + r() * 15), clamp(140 + r() * 30), clamp(30 + r() * 20)];
    return [clamp(10 + r() * 20), clamp(60 + y * 2 + r() * 30), clamp(140 + y + r() * 40)];
  },
  gray: (x, y, r) => { const v = clamp(40 + x * 2 + r() * 8); return [v, v, clamp(v + 2)]; },
};

const roleNames = [
  'primary_palette_key_color', 'secondary_palette_key_color', 'tertiary_palette_key_color',
  'neutral_palette_key_color', 'neutral_variant_palette_key_color', 'error_palette_key_color',
  'background', 'on_background', 'surface', 'surface_dim', 'surface_bright',
  'surface_container_lowest', 'surface_container_low', 'surface_container',
  'surface_container_high', 'surface_container_highest', 'on_surface', 'surface_variant',
  'on_surface_variant', 'inverse_surface', 'inverse_on_surface', 'outline', 'outline_variant',
  'shadow', 'scrim', 'surface_tint', 'primary', 'on_primary', 'primary_container',
  'on_primary_container', 'inverse_primary', 'secondary', 'on_secondary', 'secondary_container',
  'on_secondary_container', 'tertiary', 'on_tertiary', 'tertiary_container',
  'on_tertiary_container', 'error', 'on_error', 'error_container', 'on_error_container',
  'primary_fixed', 'primary_fixed_dim', 'on_primary_fixed', 'on_primary_fixed_variant',
  'secondary_fixed', 'secondary_fixed_dim', 'on_secondary_fixed', 'on_secondary_fixed_variant',
  'tertiary_fixed', 'tertiary_fixed_dim', 'on_tertiary_fixed', 'on_tertiary_fixed_variant',
];
const camel = (s) => s.replace(/_([a-z])/g, (_, c) => c.toUpperCase());
const colors = new MaterialDynamicColors();
const hex = (argb) => ((argb >>> 0) & 0xffffff).toString(16).padStart(6, '0');

const golden = { roles: roleNames, images: {}, schemes: [] };
const seeds = [];
for (const [name, fn] of Object.entries(images)) {
  const r = mulberry32(name.length * 7919 + name.charCodeAt(0));
  const buf = Buffer.alloc(W * H * 4);
  const pixels = [];
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) {
    const [R, G, B] = fn(x, y, r);
    const argb = (0xff000000 | R << 16 | G << 8 | B) >>> 0;
    buf.writeUInt32LE(argb, (y * W + x) * 4);
    pixels.push(argb);
  }
  fs.writeFileSync(path.join(out, name + '.argb'), buf);
  setSeed(0x42688);
  const quantized = QuantizerCelebi.quantize(pixels, 128);
  const ranked = Score.score(quantized);
  golden.images[name] = {
    quantized: [...quantized.entries()].map(([c, n]) => [hex(c), n]),
    ranked: ranked.map(hex),
  };
  seeds.push(ranked[0] >>> 0);
}
seeds.push(0xff4285f4, 0xffb3261e, 0xffd4c000, 0xff00ffff);
const variants = ['MONOCHROME', 'NEUTRAL', 'TONAL_SPOT', 'VIBRANT', 'EXPRESSIVE', 'FIDELITY', 'CONTENT', 'RAINBOW', 'FRUIT_SALAD'];
for (const s of seeds) for (const v of variants) for (const dark of [false, true]) for (const contrast of [-1, 0, 0.5, 1]) {
  const scheme = new DynamicScheme({ sourceColorHct: Hct.fromInt(s), variant: Variant[v], contrastLevel: contrast, isDark: dark });
  golden.schemes.push({
    seed: hex(s), variant: v, dark, contrast,
    colors: roleNames.map((n) => hex(colors[camel(n)]().getArgb(scheme))).join(' '),
  });
}
fs.writeFileSync(path.join(out, 'golden.json'), JSON.stringify(golden, null, 1) + '\n');
