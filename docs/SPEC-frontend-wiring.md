# SPEC：把原型 v2.1 前端接到 i3-composer 后端

> 目标读者：下一个会话的实现者（无需回顾历史对话）。
> 本文件自成一体，照着做即可。

---

## 1. 背景与目标

`/Users/zhangkun/Documents/企业AI落地/i3-composer_交互原型_v2.1.html` 是一个自包含单文件原型（3418 行，HTML + 内联 CSS/JS）。它用 `var DB`、`var BACKLOG` 两份内存数据模拟全部状态，写 localStorage 做「刷新不丢」。

后端 `/Users/zhangkun/projects/i3-composer` 已实现并端到端跑通（真实 Dapr）。当前唯一缺口：**前端还在自己编状态，没接后端**。

本 spec 的唯一目标：让原型页面读真实投影、写真实接口，做到「所见即账本」。

---

## 2. 当前后端状态（已验证，不要重做）

### 2.1 已跑通的链路

```
BA 建任务        201  {"id":"US-04"}
DA 发起          202  instanceID = itemID（Dapr 409 即并发锁）
workflow         RUNNING → 六步 done → 停在发布闸
DA 审批          403  权限不足
SA 审批          202
workflow         COMPLETED  {"commit":"1.0.2","ok":true,"tag":"v1.0.0"}
投影            run=published / tag=v1.0.0 / version=1.0.2
已发布再发起     400  已发布（v1.0.0），如需变更请新建工作项
```

- `go build ./...` / `go vet ./...` / `gofmt -l .` 全干净
- `go test ./...` 全过（domain 7 + store 8 + bff 8）
- daprd 日志 0 error

### 2.2 运行方式

```bash
cd /Users/zhangkun/projects/i3-composer

docker compose up -d          # placement + scheduler + daprd
go run ./cmd/i3-composer      # 必须在宿主机跑

curl -s http://127.0.0.1:8080/api/health
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3500/v1.0/healthz   # 期望 204
```

**关键环境事实（踩过坑，别改回去）：**

1. `docker-compose.yml` 的 daprd 必须有 `--app-channel-address=host.docker.internal`。否则容器内探 `127.0.0.1:8080` 永远不通，日志刷 `waiting for application to listen`。
2. `components/statestore.yaml` 用 `state.redis`（不是 sqlite），且必须 `actorStateStore: "true"`（工作流引擎硬要求）。
3. Dapr 状态并发模型字符串是 `"first-write"`，不是 `"first-write-wins"`（后者 500）。代码已封装成 `dapr.ConcurrencyFirstWrite`，新代码一律引用常量。
4. 种子 = `pbc.created` + v1.0.1 完整制品基线 + 基线提交（`seedBaseline`）+ 3 个工作项。没有基线，任何提交都会被「引用闭合」闸拒绝（`backlog 未入树`），这是闸的正确行为。

---

## 3. 后端 API 契约（唯一权威，前端只消费这些）

所有接口以 `/api` 开头。**权限只由服务端签名会话 cookie（`i3_sess`）决定，请求体里的 role / canApprove 一律不采信。**

### 3.1 会话与权限

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/role` | body `{"role":"BA"\|"PA"\|"DA"\|"SA"}`，下发签名 cookie，返回 `{role,name,acts}` |
| `GET` | `/api/health` | `{ok:true,time}` |

四个角色与动作（对应原型 `ROLES`，**完全一致，不要改**）：

| 角色 | 名称 | acts |
|---|---|---|
| BA | BA 业务 | brd, backlog, acceptAT, view |
| PA | PA 产品 | pbc, store, acceptProto, view |
| DA | DA 交付 | build, test, deploy, view |
| SA | SA 系统 | config, tag, view |

cookie 缺失/签名不符时**默认 BA**（只读视角起点）。

### 3.2 读

| 方法 | 路径 | 返回 |
|---|---|---|
| `GET` | `/api/overview?pbc=fee` | `{role,name,acts,pbc,items:[Task],progress:{pbc,head,tags,commits}}` |
| `GET` | `/api/tasks?pbc=fee&tab=todo\|review\|mine` | `{tab,items:[Task]}` |
| `GET` | `/api/items/{id}?pbc=fee` | `{item:Task,facts:[{seq,at,type,actor}]}` |

`Task` 的 JSON 字段（**注意：`t` 是标题、`pt` 是点数，沿用原型命名**）：

```json
{
  "id":"US-04","t":"账单分期与滞纳金计算","pt":3,"st":"doing","owner":"—",
  "kind":"Story","pbc":"fee","type":"feature","mode":"fast","env":"test",
  "run":"waiting","step":6,
  "steps":[{"k":"artifacts","n":"生成制品","d":"...","st":"done","at":"11-22 15:18"},
           {"k":"publish","n":"发布生产","d":"...","st":"todo","human":true}],
  "version":"1.0.2","tag":"v1.0.0","url":"https://...","by":"BA",
  "blocked":"...","blockedBy":"...","at":"11-23 20:20"
}
```

`run` 取值（**只有这些**）：`idea` | `running` | `waiting` | `paused` | `blocked` | `published`
（原型里还有 `draft`，后端不用，前端映射时按 `idea` 处理。）

`st`（工作项状态）：`todo` | `doing` | `done`
`steps[].st`：`todo` | `doing` | `done` | `blocked`

### 3.3 写

| 方法 | 路径 | 权限 | body | 成功 |
|---|---|---|---|---|
| `POST` | `/api/tasks?pbc=fee` | BA（backlog） | `{title,type?,env?,mode?}` | `201 {"id":"US-04"}` |
| `POST` | `/api/items/{id}/run?pbc=fee` | DA（build） | 无 | `202 {"id","instance"}` |
| `POST` | `/api/items/{id}/answer?pbc=fee` | SA（tag） | `{approve:bool,note?}` | `202 {"id","approve"}` |

错误统一为 `{"error":"中文说明"}`，状态码：`403` 权限不足 / `400` 请求不合法 / `404` 不存在 / `500` 内部错误。

关键业务护栏（前端只需把错误原文 toast 出来）：
- `run` 只允许 `idea` / `blocked` 的工作项；`running`/`waiting` → 400「已在执行中」
- `published` → 400「已发布（tag），如需变更请新建工作项」
- `answer` 只允许 `run=waiting`，否则 400

### 3.4 实时通知

`GET /api/timeline/stream?pbc=fee` —— SSE。
- 首帧 `event: hello\ndata: {}`
- 变更时 `event: change\ndata: {"pbc":"fee","type":"item.run.started","data":{...}}`
- 25s keepalive

**语义很重要：SSE 只通知「有变化了」，不携带账本内容。收到 change 后必须重新 GET 全量投影。**

### 3.5 当前缺口（本 spec 要解决的）

后端**没有**返回完整 `View`（versions / commits / deploys / annos）的接口，而原型的「方案」「发布」等页面要读这些。

**需要新增**（见第 5 节）：`GET /api/state?pbc=fee`。

---

## 4. 原型的数据模型（要映射的那一侧）

原型全部状态在 `DB`（项目/PBC/版本线/提交/部署/批注）和 `BACKLOG`（工作项表）里。
原型关键函数位置（行号以 v2.1 为准）：

| 行 | 内容 |
|---|---|
| 543 | `var DB = {project, pbcs:{fee:{versions,commits,deploys,annos}}}` |
| 611 | `var S = {...}` 视图状态 |
| 611 | `localStorage` 快照 `SNAP_KEY='i3:snap'` |
| 738 | `function go(v){S.view=v;refreshAll();}` |
| 739 | `function refreshAll()` |
| 2831 | `var BACKLOG=[...]` |
| 2615 | `var ROLES` |
| 3030 | `function item(id)` |
| 3309 | `function openItem(id)` |
| ~3018 | `mkSteps(env)` 七步 |
| ~3130 | `launchStep / execStep / intervene / resumeItem / publishItem` |
| 2522 | `doCommit()` |
| ~2690 | `doTag(no)` |

### 4.1 字段映射表（后端 Task → 原型 BACKLOG 项）

| 后端 | 原型 | 备注 |
|---|---|---|
| `id` | `id` | 同名 |
| `t` | `t` | 同名（注意后端叫 `t` 不叫 `title`） |
| `pt` | `pt` | |
| `st` | `st` | |
| `owner` | `owner` | |
| `kind` | `kind` | |
| `pbc` | `pbc` | |
| `type` | `type` | |
| `mode` | `mode` | |
| `env` | `env` | |
| `run` | `run` | 见 §3.2 取值 |
| `step` | `step` | |
| `steps` | `steps` | **同构**：k/n/d/st/at/human 完全一致 |
| `version` | `version` | |
| `tag` | `tag` | |
| `url` | `url` | |
| `by` | `by` | |
| `blocked` | `blocked` | |
| `blockedBy` | `blockedBy` | |
| `at` | `at` | |
| （无） | `due` | 原型独有，映射时可留 `'—'`，或后续从后端加 |
| （无） | `testResult` | 原型独有，后端暂不产出；建议前端显示时容错 |
| （无） | `interventions` | 原型独有；后端审计信息在 facts 里，建议先置空数组 |

**结论：`steps` 是同构的，这一条省掉了大量适配工作。**

### 4.2 后端 View → 原型 DB.pbcs.fee

| 后端 View 字段 | 原型 | 备注 |
|---|---|---|
| `pbc` / `name` | `pbcs.fee.id` / `.name` | |
| `versions` `map[Artifact][]Version` | `pbcs.fee.versions` | 同构：`{t,v,at,by,source,df,st,note,digest}` |
| `commits` `[]Commit` | `pbcs.fee.commits` | 后端新的在前，原型也是 `unshift` → 顺序一致 |
| `tags` `[]string` | 由 commits 的 `tag` 字段体现 | 原型 `tagList()` 是从 commits 过滤 tag；后端 tags 是独立列表，写回时挂到对应 commit 上 |
| `builds` `map[commitNo]digest` | commit.digest | |
| `deploys` `[]Deploy` | `pbcs.fee.deploys` | 同构：`{tag,commit,url,at,by,status}` |
| `annos` `[]Anno` | `pbcs.fee.annos` | 同构：`{x,y,w,h,text,at,by,round}` |
| `items` `map[string]*Item` | `BACKLOG`（过滤被 Worker 接管的 Story） | |
| `seq` | — | 事实条数，可做「账本长度」展示 |

---

## 5. 实施计划（按顺序做，每步可独立验证）

### 步骤 1：后端新增 `GET /api/state`

**文件**：`internal/bff/server.go`

在 `Handler()` 里加一行：

```go
m.HandleFunc("GET /api/state", s.wrap(s.state))
```

新增 handler（直接序列化 `*store.View`，字段名已经是原型要的形状）：

```go
// state 返回完整投影：版本线、提交线、部署、批注、工作项。
// 前端「方案 / 发布」页需要这些，而 overview 只给工作项摘要。
// 注意：这不是另一份真相——它就是 Project(facts) 的输出，和 /overview 同源。
func (s *Server) state(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}
```

**验证**：

```bash
curl -s "http://127.0.0.1:8080/api/state?pbc=fee" | python3 -m json.tool | head -40
# 期望：versions 里 6 类制品（prd/proto/ad/at/backlog/code）、commits 至少 1 条（基线 1.0.1）
```

补测试 `internal/bff/server_test.go`：

```go
func TestStateReturnsFullProjection(t *testing.T) {
    h := newHarness(t)
    seedItem(t, h.st, store.Item{ID:"US-01", Title:"t", Kind:"Story", PBC:"fee",
        Type:"feature", Env:"test", Mode:"fast", Run:"idea", By:"BA"})
    resp, out := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
    if resp.StatusCode != http.StatusOK { t.Fatalf("应 200，实际 %d", resp.StatusCode) }
    if _, ok := out["versions"]; !ok { t.Fatal("state 必须包含 versions") }
    if _, ok := out["items"]; !ok { t.Fatal("state 必须包含 items") }
}
```

---

### 步骤 2：把原型 HTML 放进 `web/` 并由 BFF 托管

**为什么复制而不是改造原文件**：原文件在 `~/Documents` 下，是设计稿，保持只读。

```bash
cp "/Users/zhangkun/Documents/企业AI落地/i3-composer_交互原型_v2.1.html" \
   /Users/zhangkun/projects/i3-composer/web/index.html
```

`web/` 目前是空目录，已经在仓库里。

**新增** `web/adapter.js`（独立文件，不改 index.html 里的原型代码——除了末尾加一行 `<script>`，见步骤 3）。

**托管**：`cmd/i3-composer/main.go` 当前**完全没有**静态托管，`/api` 的 9 条路由全部挂在 `bff.Server.Handler()`（`internal/bff/server.go:35-46`）返回的 `ServeMux` 上。

**具体做法**（二选一，推荐 A）：

**A. 在 bff.Server.Handler 里挂静态**（改动集中）：

```go
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	// ... 现有 /api 路由 ...
	// 静态：web/ 目录。只有 index.html 一个文件 + adapter.js。
	m.Handle("/", http.FileServer(http.Dir("web")))
	return m
}
```

**B. 在 main.go 外面包一层**：判断前缀 `/api` 走 BFF，否则走 `http.FileServer`。

**注意（A 方案的坑）**：`http.Dir("web")` 是相对**进程工作目录**的，所以必须从 `/Users/zhangkun/projects/i3-composer` 起 `go run ./cmd/i3-composer`。如果以后要改成可执行文件分发，换成 `embed.FS` + `//go:embed` 更稳（本次不做）。

**验证**：浏览器打开 `http://127.0.0.1:8080/` 能看到原型页面（此时还是假数据，正常）。

---

### 步骤 3：写 `web/adapter.js`（核心工作量）

**加载方式**：在 `web/index.html` 的 `</body>` 前加：

```html
<script src="/adapter.js"></script>
```

必须放在原型主 `<script>` **之后**（原型把自己的函数和 `var DB` 都挂在全局，adapter 要覆盖它们）。

**adapter.js 要做四件事：**

#### 3.1 拉真实状态，覆盖 `DB` 与 `BACKLOG`

```js
const API = '';                    // 同源
const PBC = 'fee';                 // 单 PBC 阶段

async function fetchState() {
  const [st, ov] = await Promise.all([
    fetch(`${API}/api/state?pbc=${PBC}`).then(r => r.json()),
    fetch(`${API}/api/overview?pbc=${PBC}`).then(r => r.json()),
  ]);
  applyToPrototype(st, ov);
}

function applyToPrototype(st, ov) {
  // BACKLOG：由 items 重建。保留原型专有字段的默认值。
  const items = ov.items.slice().sort((a, b) => a.id.localeCompare(b.id));
  BACKLOG.length = 0;
  items.forEach(it => {
    BACKLOG.push({
      ...it,
      due: '—',
      interventions: [],
      // 后端 run 里没有 draft；原型有，容错成 idea 已由后端保证
    });
  });

  // DB.pbcs.fee：整棵视图状态
  const p = DB.pbcs[PBC];
  p.versions = st.versions || {};
  p.commits  = st.commits  || [];
  p.deploys  = st.deploys  || [];
  p.annos    = st.annos    || [];
  p.name     = st.name || p.name;

  // tags：原型 tagList() 从 commits 里取 tag，所以把 tags 挂回 commit
  // 后端 commits[i].tag 已经带了（apply FactTagCreated 时会写回），一般无需另处理
}
```

**坑**：原型 `BACKLOG` 里有非 Story 的行（`T-0xx` 类型的 Task，人负责的项），后端目前只有 Story。**先只渲染 Story**，Task 细项后续再做（写进 §7 未决清单）。

#### 3.2 覆盖 `refreshAll()`，改成「先拉后端再渲染」

原型 `refreshAll()` 是本地的 `renderXxx()` 串。改成：

```js
const origRefreshAll = refreshAll;
let refreshing = false;

async function refreshAll() {
  if (refreshing) return;              // 防重入
  refreshing = true;
  try {
    await fetchState();
    origRefreshAll.call(this);         // 注意 ROLES/S.me 仍是前端会话态
  } finally {
    refreshing = false;
  }
}
```

**注意**：原型里 `refreshAll()` 被大量同步调用（按钮 onclick 里直接写 `refreshAll()`）。改成 async 后，这些调用点**不需要改**——JS 里 `refreshAll()` 返回 Promise 也会正常执行，只是渲染发生在下一个 microtask。但要确认：没有代码依赖 `refreshAll()` 的同步完成（例如紧接着读 DOM）。**实测检查 `doCommit()` / `publishItem()` 等；如果发现依赖，改成在 adapter 里重写这些函数，用 `await` 后再调原型渲染。**

`saveSnap()` 要**禁用**（否则本地快照会覆盖真实数据）：

```js
_snapOn = false;                       // 原型里控制 saveSnap 的开关
function saveSnap() { /* 接后端后不再写本地快照 */ }
```

并且**不要**调 `loadSnap()`（如果有启动逻辑，覆盖成 no-op）。

#### 3.3 覆盖写操作：`runItem` / `answer` / 建任务

```js
// 发起交付：DA 权限
window.runItem = async function (id) {
  const r = await fetch(`${API}/api/items/${id}/run?pbc=${PBC}`, { method: 'POST' });
  const j = await r.json();
  if (!r.ok) return toast(j.error || '发起失败', 'err');
  toast(`${id} 已发起`, 'ok');
  await refreshAll();
};

// 发布拍板：SA 权限。原型 publishItem 是本地模拟，直接替换。
window.publishItem = async function (id) {
  const r = await fetch(`${API}/api/items/${id}/answer?pbc=${PBC}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ approve: true }),
  });
  const j = await r.json();
  if (!r.ok) return toast(j.error || '发布失败', 'err');
  toast(`${id} 已提交发布决定`, 'ok');
  await refreshAll();
};

// 拒绝发布（原型没有这个按钮，可选新增）
window.rejectPublish = async function (id, note) {
  const r = await fetch(`${API}/api/items/${id}/answer?pbc=${PBC}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ approve: false, note }),
  });
  const j = await r.json();
  if (!r.ok) return toast(j.error || '操作失败', 'err');
  toast(`${id} 发布已拒绝`, 'warn');
  await refreshAll();
};

// 建 backlog：BA 权限
window.createTask = async function (title, type, env) {
  const r = await fetch(`${API}/api/tasks?pbc=${PBC}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, type: type || 'feature', env: env || 'test' }),
  });
  const j = await r.json();
  if (!r.ok) return toast(j.error || '创建失败', 'err');
  toast(`已创建 ${j.id}`, 'ok');
  await refreshAll();
};
```

**注意**：原型里 `runItem` 被 `launchStep`、`rerunItem` 等内部函数调用，语义是「本地跑七步」。接后端后这些本地模拟**必须停用**：`launchStep`/`execStep`/`intervene`/`resumeItem` 在 adapter 里覆盖成**只调接口 + 提示**，不要真的执行本地假流程。

**最简做法**：覆盖 `execStep` 为 no-op + 覆盖 `runItem`（如上）。因为 `launchStep` 最终会调 `execStep`，但 `launchStep` 自己会先把 `state` 改成 `running`、把 step 标 `doing` —— 这会和下一次 `refreshAll()` 拉到的真实状态打架。**稳妥做法是连 `launchStep` 一起覆盖成直接调 API。**

#### 3.4 订阅 SSE

```js
function connectStream() {
  const es = new EventSource(`${API}/api/timeline/stream?pbc=${PBC}`);
  es.addEventListener('change', () => { refreshAll(); });   // 只触发重拉
  es.onerror = () => {
    // EventSource 自带重连；这里只做退避保护，不要疯狂重连
  };
}
connectStream();
```

**SSE 只是通知，不是数据源。** 不要在 change 事件里解析 data 去改状态。

#### 3.5 角色切换

原型 `setRole(id)` 只改前端 `S.me`。接后端后必须**同时**调 `/api/role`，否则后端会话仍是 BA，写操作会 403：

```js
window.setRole = async function (id) {
  const r = await fetch(`${API}/api/role`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role: id }),
  });
  if (!r.ok) return toast('角色切换失败', 'err');
  S.me = id;
  await refreshAll();
};
```

**这是最容易漏的一步**：只改前端 `S.me` 而不同步 cookie，前端显示「SA 可以拍板」，点下去却 403。

---

## 6. 验收标准（实现完必须逐条跑）

### 6.1 页面加载

打开 http://127.0.0.1:8080/

- [ ] 工作台显示 3 个种子工作项（US-01 published / US-02 waiting / US-03 idea）
- [ ] 进度条显示 head=1.0.1、commits=1
- [ ] DevTools → Network 里能看到 /api/state 与 /api/overview
- [ ] Console 无报错
- [ ] localStorage 的 i3:snap 不再被写入（刷新前后对比）

### 6.2 建任务（BA）

1. 切换到 BA
2. 建一条新工作项，标题「端到端验证」
- [ ] toast 显示「已创建 US-04」
3. GET /api/overview?pbc=fee 能查到 US-04
4. 刷新浏览器（F5）→ 新工作项仍在

刷新后还在 = 它是从事实日志来的，不是前端内存。这是本 spec 的核心验收点。

### 6.3 发起（DA）

1. 切到 DA
2. 对 US-03 点「执行」
- [ ] 202，toast 正常
- [ ] 几秒内页面自动逐步推进（SSE 驱动，无需手动刷新）
- [ ] 最终停在 run=waiting、step=6、publish 步骤为 todo 且标记 human

3. 看 daprd 日志：docker logs i3-composer-daprd-1 --tail 50 | grep -i workflow 能看到执行

### 6.4 越权被拒

1. 保持 DA 身份，对一个 waiting 项点「发布」

- [ ] 界面提示「权限不足」（后端 403）
- [ ] 关键：界面状态不能变成 published（说明前端没有先乐观更新）

### 6.5 审批（SA）

1. 切到 SA
2. 对 waiting 项点「发布」
- [ ] 202
- [ ] 几秒内 run 变 published，显示 tag 与 version
- [ ] GET /api/items/US-03?pbc=fee 的 facts 里能看到 publish.decided 与 item.published

### 6.6 重启不丢

Ctrl-C 停掉 go run，再重新 go run。

- [ ] 刷新页面，所有状态与重启前一致
- [ ] 证明 BFF 不持有业务状态（符合防腐层定位）

### 6.7 回归

go build ./... && go vet ./... && gofmt -l . && go test ./...

- [ ] 全绿

---

## 7. 明确不做（本次范围外，别顺手加）

| 项 | 原因 |
|---|---|
| 多 PBC 切换 | 后端 pbc 参数已支持，但种子只有 fee；先单 PBC 打通 |
| Task 细项（T-0xx）渲染 | 后端只有 Story；原型里的 Task 是 mock，不要在适配层伪造 |
| intervene / resumeItem 介入 | 后端没有对应接口（介入 = 暂停 + 改制品 + 续跑），需新增 fact + activity，另开 spec |
| 制品在线编辑、批注写入 | 后端 annos 只读 |
| 真实构建（Jenkins） | mockBuilder 是唯一大块，另开 spec |
| 视觉还原闸的真实比对 | 后端硬编码 unhandled=0 |
| 登录 / SSO | 现在是签名 cookie + 角色选择；接 SSO 只换 Sessions.Get |

---

## 8. 已知陷阱（都是这一轮真实踩过的）

1. **权限只认 cookie。** 前端若只在本地改 `S.me`，后端仍是 BA/DA，写操作 403。`setRole` 必须同时打 `/api/role`。
2. **`refreshAll` 变 async 后的同步依赖。** 原型里按钮 onclick 直接调 `refreshAll()`；个别函数（`doCommit`、`publishItem`）可能随后同步读 DOM。发现即重写该函数。
3. **不要本地乐观更新。** 写操作一律「发请求 → 成功后再 refreshAll」。否则越权时界面已经变了（6.4 会抓出来）。
4. **SSE 只做通知。** 不要在 change 事件里解析 data 改状态。
5. **`saveSnap`/`loadSnap` 必须停用。** 否则本地快照会掩盖后端真实状态，刷新时还可能把假数据写回去。
6. **静态托管别抢 `/api`。** ServeMux 里 `/api/...` 更具体，优先匹配；`/` 兜底静态。但如果用 `http.FileServer` 包在 BFF 外层，要显式判断前缀。
7. **Dapr 并发字符串**：新代码一律用 `dapr.ConcurrencyFirstWrite`，不要写裸字符串。
8. **`web/index.html` 是从设计稿复制的副本。** 原型原文（`~/Documents/...`）保持只读，不要改它。

---

## 9. 关键文件清单

**后端（已存在，步骤 1 只改两处）**
- `internal/bff/server.go` — 路由 + handler（加 `/api/state`）
- `internal/bff/server_test.go` — 测试（加 1 个）
- `internal/store/project.go` — `View`/`Item`/`Step` 的 JSON 形状（只读参考）
- `internal/bff/session.go` — 角色与权限（只读参考）
- `cmd/i3-composer/main.go` — 启动 + 种子 + CORS

**前端（步骤 2-3 新建）**
- `web/index.html` — 原型副本
- `web/adapter.js` — 适配层（新建）

---

## 10. 一句话总结

后端已经是权威账本；前端要做的是**把 `DB` 与 `BACKLOG` 从「本地编造」换成「每次从 `/api/state` + `/api/overview` 重放」，把三个写操作（建、发起、拍板）换成真实接口调用，并让角色切换同时作用于 cookie**。`steps` 数组两边同构，是最省事的地方。
