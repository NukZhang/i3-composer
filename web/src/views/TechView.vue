<script setup>
import PageHeader from '../components/PageHeader.vue';
import { S } from '../store.js';
import { icon } from '../icons.js';

const rows = [
  ['Workflow', 'L3', 'Agent 生命周期五阶段一条 workflow。确认门可能等三天，进程可以死；回答来了用 external event 唤醒', 'Dapr durabletask（已接）'],
  ['Actors', 'L3 / L2', '一个 Agent 一个 actor（同时只跑一轮）；一个 PBC 一个 actor（不许并发提交）', '实例 ID = 工作项 ID'],
  ['Service Invocation', 'L3 ↔ PBC', 'PBC 微服务之间、BFF 与 Agent 之间互调，带 mTLS、重试、超时', 'HTTP 直连（未接）'],
  ['State', 'L3', '事实日志 append-only，投影随时可从日志重建', 'Dapr state.redis + etag CAS'],
  ['Pub / Sub', 'L3 → L4', 'commit / tag / build / deploy 事件分发给通知、看板、审计', '内部调用 + /api/timeline/stream'],
  ['Bindings 入', 'L4', '飞书、Slack、GitHub、Jira 的事件推进来', '未接'],
  ['Bindings 出', 'L4 / L1', '发卡片到聊天工具；出站调模型 API', '未接'],
  ['Configuration', 'L1 / L4', '模型路由、降级开关、渠道开关，改配置不发版', 'env + components/*.yaml'],
  ['Secrets', 'L1 / L2', '各家 API key、Git 凭据，只给需要的进程', 'Dapr secret store（未接）'],
  ['Jobs', 'L3', '超时扫描、due 提醒、每日早报', 'workflow 定时器（发布闸 30 天超时）'],
  ['Distributed lock', 'L3', '同一 (pbc, commit) 只允许一个 tag 落库', 'etag CAS / 实例 ID 锁'],
];
const mp = [
  ['工作台：我的待办 / 待评审', '/api/overview /api/tasks /api/timeline/stream', '复用', 't-ok'],
  ['项目 · BRD 版本线', '/api/brd', '已实现', 't-ok'],
  ['方案：PRD / AD / AT 编辑', 'POST /api/artifacts + 版本线 + stale', '已实现', 't-ok'],
  ['提交 / tag / 四闸', 'domain.CheckCommit + workflow', '已实现', 't-ok'],
  ['构建链路：代码→自测→镜像→部署', 'wf.Delivery + mockBuilder', '接真 CI 是唯一的大块', 't-err'],
  ['原型预览 / 还原度对比', 'annotation.added + 原型页', '部分（还原对比未接）', 't-warn'],
  ['测试报告', 'workflow 的 test 步骤', '直接复用', 't-ok'],
  ['通知 / 卡片', '/api/notifications（事实派生）', '已实现', 't-ok'],
  ['四角色与权限', 'bff.Role.Can()', '已实现', 't-ok'],
  ['Agent 编排（流程图 → digest）', 'PUT /api/agents/{id}/flow + POST .../build', '已实现', 't-ok'],
  ['知识库依赖与准确度', 'agent.kb + 失败归因 / 问题单', '已实现', 't-ok'],
  ['真人验收台（AC 判定 / 提 bug）', 'POST /api/checks/{id}/decide + defect.created', '已实现', 't-ok'],
  ['Agent 评测 / 灰度', '/api/agents/*', '已实现', 't-ok'],
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
      <div class="w"><div class="q">需求一 · 等人等三天</div><div class="a">确认门可能等到下周。Workflow 把进度落在 history 里，进程随时能死，<b>回答到了用 external event 唤醒</b>，接着原位置往下跑。</div></div>
      <div class="w"><div class="q">需求二 · 不能并发改</div><div class="a">两个人同时打 tag，必须有一个失败。Actor 按 key 串行，<b>(pbc, commit) 只有一个能被处理</b>，不用自己写锁和幂等。</div></div>
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
      <div class="tree-line"><span class="k">L3</span><span class="v">POST /api/items/US-02/run → StartWorkflow i3-delivery，实例 ID = US-02</span></div>
      <div class="tree-line"><span class="k">L2</span><span class="v">activity 生成制品 → 收敛 → 原子提交 → 测试 → 构建 → 部署 test</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">item.waiting 落账 + workflow 挂起，进程释放</span></div>
      <div class="tree-line"><span class="k">L4</span><span class="v">SSE 通知「有变化了」→ 页面重拉 /api/state，待办出现在「待我确认」</span></div>
      <div class="tree-line"><span class="k">L4</span><span class="v">人回答「发布」→ POST /api/items/US-02/answer → 落 publish.decided 事实</span></div>
      <div class="tree-line"><span class="k">L3</span><span class="v">唤醒 → 四闸复核 → 打 tag v1.0.0 → item.published</span></div>
    </div>
    <div class="arch-note">关键：<b>第 5 到 7 步之间可以隔三天</b>。这三天的等待不占进程、不烧 token，这是自研状态机最容易写错的地方。</div>
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
    <div class="arch-note">
      UI 换数据源、不动布局；后端补三样（stale、树 hash、四闸），其余复用。
      <b>唯一的大块是构建链路</b>——那要接真 CI，不是改页面能解决的。
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('alert')" /><h3>当前状态：目标形态 ≠ 已完成</h3></div>
    <div class="honest">
      <b>已接 Dapr。</b>事实日志在 Dapr state store（Redis，etag CAS 串行），workflow 走 durabletask 真实编排，
      确认门用 external event 唤醒。未接的部分：真 CI（构建是确定性 mock）、真模型调用（评测结果由外部执行器提交）、
      飞书 / 企微卡片（通知只到站内）。上表标「未接」的是差距，不是待办清单里的装饰。
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
