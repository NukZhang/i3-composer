// 图标表：路径全部来自原型 v2.3 的 ICONS，一条 path 一个图标。
// 保持单一 stroke 路径，这样 currentColor / stroke-width 由外层控制。
export const ICONS = {
  doc: 'M6 2h7l5 5v15H6z M13 2v5h5',
  layout: 'M3 4h18v16H3z M3 9h18 M9 9v11',
  layers: 'M12 3l9 5-9 5-9-5z M3 13l9 5 9-5',
  check: 'M4 12l5 5L20 6',
  list: 'M8 6h13 M8 12h13 M8 18h13 M3 6h.01 M3 12h.01 M3 18h.01',
  box: 'M3 8l9-5 9 5v8l-9 5-9-5z M3 8l9 5 9-5 M12 13v8',
  git: 'M6 3v12a3 3 0 003 3h6 M6 3a2 2 0 100 4 2 2 0 000-4z M18 15a2 2 0 100 4 2 2 0 000-4z',
  tag: 'M3 12V4h8l9 9-8 8z M7.5 7.5h.01',
  cloud: 'M17 18a4 4 0 000-8 6 6 0 10-11.3 2A3.5 3.5 0 006 18z',
  play: 'M7 4l12 8-12 8z',
  alert: 'M12 3l9 17H3z M12 9v5 M12 17h.01',
  bell: 'M18 8a6 6 0 10-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9 M10 21h4',
  plus: 'M12 5v14 M5 12h14',
  lock: 'M6 11h12v10H6z M9 11V7a3 3 0 016 0v4',
  cpu: 'M6 6h12v12H6z M9 2v3 M15 2v3 M9 19v3 M15 19v3 M2 9h3 M2 15h3 M19 9h3 M19 15h3',
  robot: 'M8 4h8v3H8z M5 7h14v12H5z M9 12h.01 M15 12h.01 M9 16h6',
  refresh: 'M3 12a9 9 0 0115-6.7L21 8 M21 3v5h-5 M21 12a9 9 0 01-15 6.7L3 16 M3 21v-5h5',
  chev: 'M9 6l6 6-6 6',
  plug: 'M9 2v6 M15 2v6 M6 8h12v3a6 6 0 01-12 0z M12 17v5',
  dot: 'M12 12h.01',
  // 原型里没有、但 v2.3 用到的
  users: 'M16 20v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2 M9 8a3 3 0 100-6 3 3 0 000 6 M22 20v-2a4 4 0 00-3-3.9 M16 2.1a4 4 0 010 7.8',
  eye: 'M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7z M12 15a3 3 0 100-6 3 3 0 000 6',
};

export function icon(name, size) {
  const s = size || 13;
  return (
    '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" ' +
    'stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
    'style="vertical-align:-2px;flex:0 0 auto"><path d="' + (ICONS[name] || ICONS.box) + '"/></svg>'
  );
}
