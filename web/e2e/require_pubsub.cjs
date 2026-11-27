async function requirePubsub(base) {
  const endpoint = new URL('/api/health', base);
  const response = await fetch(endpoint, { signal: AbortSignal.timeout(5000) }).catch((error) => {
    throw new Error(`无法访问 ${endpoint}：${error.message}`);
  });
  if (!response.ok) {
    throw new Error(`${endpoint} 返回 HTTP ${response.status}`);
  }
  const health = await response.json().catch(() => null);
  const wiring = health?.pubsub;
  if (health?.ok !== true || wiring?.checked !== true || wiring?.ok !== true) {
    throw new Error(`${endpoint} 未通过验收前置检查：${wiring?.detail || '未提供 Pub/Sub 接线诊断'}`);
  }
}

module.exports = { requirePubsub };
