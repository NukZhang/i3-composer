const assert = require('node:assert/strict');
const http = require('node:http');
const { test } = require('node:test');
const { requirePubsub } = require('./require_pubsub.cjs');

test('仅接受实际目标已核对且投递端口一致', async () => {
  for (const wiring of [
    { checked: true, ok: true },
    { checked: true, ok: false, detail: '投递端口不一致' },
    { checked: false, ok: true, detail: '读不到 sidecar 元数据' },
    null,
  ]) {
    const server = http.createServer((request, response) => {
      assert.equal(request.url, '/api/health');
      response.setHeader('Content-Type', 'application/json');
      response.end(JSON.stringify({ ok: true, pubsub: wiring }));
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    try {
      const base = 'http://127.0.0.1:' + server.address().port;
      if (wiring?.checked && wiring?.ok) {
        await requirePubsub(base);
      } else {
        await assert.rejects(requirePubsub(base), new RegExp(wiring?.detail || '未提供 Pub/Sub 接线诊断'));
      }
    } finally {
      await new Promise(resolve => server.close(resolve));
    }
  }
});
