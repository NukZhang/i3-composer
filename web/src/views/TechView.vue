<script setup>
import PageHeader from '../components/PageHeader.vue';
import { S } from '../store.js';
import { icon } from '../icons.js';

const rows = [
  ['Workflow', 'L3', '交付工作项执行生成、收敛、提交、测试、构建五步', 'Dapr durabletask（已接）'],
  ['Actors', 'L3 / L2', '同一工作项只能运行一个交付实例', '实例 ID 包含账本前缀、单元和工作项'],
  ['Service Invocation', 'L3 ↔ PBC', 'PBC 微服务之间、BFF 与 Agent 之间互调，带 mTLS、重试、超时', 'HTTP 直连（未接）'],
  ['State', 'L3', '事实日志 append-only，投影随时可从日志重建', 'Dapr state.redis + etag CAS'],
  ['Pub / Sub', 'L3 → L4', 'commit / tag / build / deploy 事件分发给通知、看板、审计', '内部调用 + /api/timeline/stream'],
  ['Bindings 入', 'L4', '飞书、Slack、GitHub、Jira 的事件推进来', '未接'],
  ['Bindings 出', 'L4 / L1', '发卡片到聊天工具；出站调模型 API', '未接'],
  ['Configuration', 'L1 / L4', '模型路由、降级开关、渠道开关，改配置不发版', 'env + components/*.yaml'],
  ['Secrets', 'L1 / L2', '各家 API key、Git 凭据，只给需要的进程', 'Dapr secret store（未接）'],
  ['Jobs', 'L3', '超时扫描、待办提醒、每日早报', '未接'],
  ['Distributed lock', 'L3', '同一 (pbc, commit) 只允许一个 tag 落库', 'etag CAS / 实例 ID 锁'],
];
const mp = [
  ['工作台：我的待办 / 我发起的', '/api/inbox + /api/timeline/stream', '已实现', 't-ok'],
  ['项目 · BRD 版本线', '/api/brd', '已实现', 't-ok'],
  ['方案：PRD / AD / AT 编辑', 'POST /api/artifacts + 版本线 + stale', '已实现', 't-ok'],
  ['提交 / tag / 四闸', 'domain.CheckCommit + POST /api/units/{id}/tag', '已实现', 't-ok'],
  ['构建与项目发布', 'wf.Delivery + POST /api/projects/{id}/orch/release', '已实现', 't-ok'],
  ['原型预览 / 还原度对比', 'annotation.added + 原型页', '部分（还原对比未接）', 't-warn'],
  ['测试报告', 'workflow 的 test 步骤', '直接复用', 't-ok'],
  ['通知 / 卡片', '/api/notifications（事实派生）', '已实现', 't-ok'],
  ['四角色与权限', 'bff.Role.Can()', '已实现', 't-ok'],
  ['Agent 编排（流程图 → digest）', 'PUT /api/agents/{id}/flow + POST .../build', '已实现', 't-ok'],
  ['知识库依赖与准确度', 'agent.kb + 失败归因 / 问题单', '已实现', 't-ok'],
  ['真人验收台（AC 判定 / 提 bug）', 'POST /api/checks/{id}/decide + defect.created', '已实现', 't-ok'],
  ['Agent 评测', 'POST /api/agents/{id}/eval', '已实现', 't-ok'],
  ['Agent 灰度分流', 'POST /api/agents/{id}/rollout', '仅记录档位，未接流量分流', 't-warn'],
];
</script>

<template>
  <PageHeader
    title="技术架构"
    sub="L1 模型不自研、L2 harness 不自研、L3 编排自研、L4 工作台自研。Dapr 在 L3 是主角，在 L4 是接线员，在 L1/L2 只是保险丝。"
  />

  <div class="card">
    <div class="ch"><span v-html="icon('layers')" /><h3>这个产品是什么</h3></div>
    <div style="font-size: 12.5px; color: var(--tx-3); line-height: 1.78">
      i3-composer 不是另起一套系统，是 <b style="color: var(--tx-1)">ai_worker 的一层界面 + 一套编排约定</b>。
      四层里只有 <b style="color: var(--accent-tx2)">L3 工作流 loop</b> 与 <b style="color: var(--accent-tx2)">L4 工作台 / 通知</b> 是本产品做的；
      L1 模型与 L2 harness 是接进来复用的，不写。
      <div style="margin-top: 8px; color: var(--tx-5)">
        本仓库的实现落点：<span class="mono" style="color: var(--info)">internal/domain</span>（纯判断）·
        <span class="mono" style="color: var(--info)">internal/store</span>（事实日志）·
        <span class="mono" style="color: var(--info)">internal/wf</span>（编排）·
        <span class="mono" style="color: var(--info)">internal/bff</span>（视图与权限）·
        <span class="mono" style="color: var(--info)">web</span>（Vue 工作台）。
      </div>
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('box')" /><h3>Dapr 构建块落在哪一层</h3><span class="spacer" /><span class="small muted">右列是真实实现</span></div>
    <table class="tb">
      <thead><tr><th style="width: 118px">构建块</th><th style="width: 70px">层</th><th>在本产品里解决什么</th><th style="width: 235px">现状</th></tr></thead>
      <tbody>
        <tr v-for="r in rows" :key="r[0]">
          <td><b style="color: var(--info); font-weight: 550">{{ r[0] }}</b></td>
          <td class="mono small" style="white-space: nowrap">{{ r[1] }}</td>
          <td class="small muted">{{ r[2] }}</td>
          <td class="small muted">{{ r[3] }}</td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('check')" /><h3>为什么是 Dapr：三条硬需求正好对上</h3></div>
    <div class="why3">
      <div class="w"><div class="q">需求一 · 交付可恢复</div><div class="a">Workflow 记录 activity 的执行历史，支持进程重启后的恢复与重试。用户在验证页对构建完成的提交验收。</div></div>
      <div class="w"><div class="q">需求二 · 并发写入有保护</div><div class="a">工作流实例 ID 防止同一工作项重复发起。打 tag 的门禁证据和 tag 通过账本 CAS 一起写入。</div></div>
      <div class="w"><div class="q">需求三 · 换平台不改内核</div><div class="a">今天飞书、明天企微、后天内部 IM。Binding 与 Pub/Sub 换插件，<b>内核代码一行不动</b>。</div></div>
    </div>
    <div class="arch-note" style="margin-top: 9px">
      这三条不是为 Dapr 找理由，是内核本来就写死的约束：日志唯一权威、判断是纯函数、插件可换。
      <b>Dapr 只是把这些约束交给基础设施，省掉自己实现 durable execution 的活。</b>
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('git')" /><h3>一条 backlog 的跨层调用链</h3><span class="spacer" /><span class="small muted">左列是层</span></div>
    <div class="tree-box">
      <div class="tree-line"><span class="k">L4</span><span class="v">POST /api/tasks {title:"管家批量生成缴费单", pbc:"fee"}　← 人或飞书卡片都能提</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">fact #1201 item.created → 投影更新（State 只追加）</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">POST /api/items/US-02/run → StartWorkflow i3-delivery</span></div>
      <div class="tree-line"><span class="k">L2</span><span class="v">activity 生成制品 → 收敛 → 原子提交 → 测试 → 构建</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">item.verified 落账，workflow 完成</span></div>
      <div class="tree-line"><span class="k">L4</span><span class="v">SSE 通知刷新投影，工作项可前往验证页</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">BA 验收后，SA 调用 POST /api/units/{id}/tag，写入 gate.evaluated 与 tag.created</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">SA 调用 POST /api/projects/{id}/orch/release，发起锁定 tag 与 digest 的发布批次</span></div>
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('robot')" /><h3>原型 → 本仓库的实现落点</h3><span class="spacer" /><span class="small muted">「改造 bff 即可」的具体含义</span></div>
    <table class="tb">
      <thead><tr><th style="width: 226px">原型里的视图</th><th>后端落点</th><th style="width: 200px">状态</th><th style="width: 60px">量</th></tr></thead>
      <tbody>
        <tr v-for="r in mp" :key="r[0]">
          <td class="small">{{ r[0] }}</td>
          <td class="small muted">{{ r[1] }}</td>
          <td class="small muted">{{ r[2] }}</td>
          <td><span class="tag" :class="r[3]" style="font-size: 9px">{{ r[3] === 't-ok' ? '已实现' : r[3] === 't-err' ? '大' : '部分' }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('alert')" /><h3>当前状态：目标形态 ≠ 已完成</h3></div>
    <div class="honest">
      <b>已接 Dapr。</b>事实写入 Redis，交付五步由 durabletask 编排。构建使用本机 Go 工具链，部署由项目发布批次执行。
      交付构建的外部 CI、模型调用、飞书和企微卡片、Agent 灰度分流仍待接入。
    </div>
    <div class="row" style="margin-top: 11px; gap: 10px; align-items: flex-start">
      <div style="flex: 1; font-size: 11px; color: var(--tx-8b); line-height: 1.7">
        <b style="color: var(--tx-4)">账本长度</b>：seq {{ (S.state && S.state.seq) || 0 }} 条事实 ·
        {{ S.units.length }} 个交付单元 · {{ S.inbox.length }} 条工作项。
        <br />前端<b style="color: var(--tx-5)">不写本地快照</b>：刷新即重放，多人看到同一份。
      </div>
    </div>
  </div>
</template>
