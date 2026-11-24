// 极简 Markdown 渲染：只支持 PRD/AD/AT/BRD 用到的子集。
// 不用第三方库：产出的 HTML 会直接进 v-html，任何多余能力都是注入面。
function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

export function md2html(src) {
  const lines = String(src || '').split('\n');
  const out = [];
  let i = 0;
  const inl = (s) => {
    let x = esc(s);
    x = x.replace(/`([^`]+)`/g, '<code>$1</code>');
    x = x.replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
    x = x.replace(/(^|[^*])\*([^*]+)\*/g, '$1<i>$2</i>');
    return x;
  };
  while (i < lines.length) {
    const l = lines[i];
    if (/^```/.test(l)) {
      const buf = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(esc(lines[i++]));
      i++;
      out.push('<pre><code>' + buf.join('\n') + '</code></pre>');
      continue;
    }
    if (/^\s*$/.test(l)) { i++; continue; }
    const hh = /^(#{1,4})\s+(.*)$/.exec(l);
    if (hh) {
      const lv = hh[1].length;
      out.push('<h' + lv + '>' + inl(hh[2]) + '</h' + lv + '>');
      i++;
      continue;
    }
    if (/^\s*[-*]\s+/.test(l)) {
      const its = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        its.push('<li>' + inl(lines[i].replace(/^\s*[-*]\s+/, '')) + '</li>');
        i++;
      }
      out.push('<ul>' + its.join('') + '</ul>');
      continue;
    }
    if (/^>/.test(l)) {
      const qs = [];
      while (i < lines.length && /^>/.test(lines[i])) qs.push(inl(lines[i++].replace(/^>\s?/, '')));
      out.push('<blockquote>' + qs.join('<br>') + '</blockquote>');
      continue;
    }
    if (/^\|/.test(l)) {
      const rows = [];
      while (i < lines.length && /^\|/.test(lines[i])) rows.push(lines[i++]);
      const body = rows
        .filter((r) => !/^\|[\s\-:|]+\|?\s*$/.test(r))
        .map((r) => r.replace(/^\||\|$/g, '').split('|').map((x) => x.trim()));
      let tb = '<table>';
      body.forEach((c, ri) => {
        tb += '<tr>' + c.map((x) => (ri === 0 ? '<th>' : '<td>') + inl(x) + '</td>').join('') + '</tr>';
      });
      out.push(tb + '</table>');
      continue;
    }
    out.push('<p>' + inl(l) + '</p>');
    i++;
  }
  return out.join('');
}

export function plainTitle(md, fallback) {
  const first = String(md || '').split('\n')[0] || '';
  return first.replace(/^#+\s*/, '') || fallback;
}
