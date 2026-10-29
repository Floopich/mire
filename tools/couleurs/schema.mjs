// Schema de couleurs Material 3 de Mire.
//
// Utilisation : npm install && npm run schema > schema.json
// Le paquet 0.4.0 de material-color-utilities importe ses modules sans
// extension : esbuild les resout le temps de l'execution.
import * as m from '@material/material-color-utilities';
const seed = m.argbFromHex(process.argv[2] || '#E8817F');
const hex = (a) => m.hexFromArgb(a).toUpperCase();
const roles = ['primary','onPrimary','primaryContainer','onPrimaryContainer','secondary','onSecondary','secondaryContainer','onSecondaryContainer','tertiary','onTertiary','tertiaryContainer','onTertiaryContainer','error','onError','errorContainer','onErrorContainer','surface','onSurface','onSurfaceVariant','surfaceDim','surfaceBright','surfaceContainerLowest','surfaceContainerLow','surfaceContainer','surfaceContainerHigh','surfaceContainerHighest','outline','outlineVariant','inverseSurface','inverseOnSurface','inversePrimary','scrim','shadow'];
const out = {};
for (const dark of [false, true]) {
  const s = new m.SchemeTonalSpot(m.Hct.fromInt(seed), dark, 0, '2025', 'phone');
  const mdc = new m.MaterialDynamicColors();
  for (const r of roles) {
    const dc = mdc[r] ? mdc[r]() : null;
    if (!dc) { console.error('absent', r); continue; }
    (out[r] ||= {})[dark ? 'dark' : 'light'] = hex(dc.getArgb(s));
  }
}
// Couleurs d'etat, roles M3 « couleur personnalisee ». Elles ne sont pas
// harmonisees avec la source : une source chaude rapprocherait l'avertissement
// du critique, et les etats doivent rester distincts quelle que soit la marque.
const status = { bon: '#2E7D32', tolere: '#1E6BC4', avertissement: '#C58F00', critique: '#C62828' };
for (const [k, h] of Object.entries(status)) {
  const pal = m.TonalPalette.fromInt(m.argbFromHex(h));
  out[k] = { light: hex(pal.tone(40)), dark: hex(pal.tone(80)) };
  out['on-' + k] = { light: hex(pal.tone(100)), dark: hex(pal.tone(20)) };
  out[k + '-container'] = { light: hex(pal.tone(90)), dark: hex(pal.tone(30)) };
  out['on-' + k + '-container'] = { light: hex(pal.tone(10)), dark: hex(pal.tone(90)) };
}
console.log(JSON.stringify(out, null, 1));
