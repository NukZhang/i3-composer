# 前端：Vue 3 工作台（原型 v2.3）

> 旧方案（让 `web/index.html` 这份原型副本 + `web/adapter.js` 改 DOM）已废弃并归档到
> `docs/prototypes/i3-composer_v2.1-wired.html`。现在的前端是 `web/` 下的 Vue 单页应用，
> 界面与交互以 `docs/prototypes/i3-composer_v2.3.html` 为准。

---

## 1. 跑起来

```bash
cd /Users/nukzhang/projects/i3-composer

docker compose up -d                      # redis + placement + scheduler + daprd
(cd web && npm ci && npm run build)       # 产物落在 web/dist，BFF 从仓库根托管它
go run ./cmd/i3-composer                  # 必须在仓库根起跑：静态根是相对路径 web/dist
```

打开 http://127.0.0.1:8080 。开发时也可以 `cd web && npm run dev`（5173 端口，`/api` 代理到 8080）。

`web/dist` 与 `web/node_modules` 都不进仓库（见 `.gitignore`）：**首次运行、或改完前端之后，必须 `npm run build`**，否则 BFF 的 `spa()` 找不到 `index.html`。

## 2. 目录

| 路径 | 作用 |
|---|---|
| `web/src/main.js` | 挂载 `App.vue`，引入 `styles/app.css`（样式逐条搬自原型 `<style>`） |
| `web/src/App.vue` | 顶栏（角色切换 / 主题 / Agent 工厂入口）、侧栏、舞台、右栏、toast；`onMounted` 拉全量 + 连 SSE |
| `web/src/store.js` | 唯一的响应式状态 `S`：`view / pbcId / agentId / role / state / units / brd / agents / facts …`，`refresh()` 串行重拉，`connect()` 订阅 SSE |
| `web/src/api.js` | 与 BFF 的全部读写；`stream()` 只通知「有变化」，不携带账本内容 |
| `web/src/derive.js` | 纯派生：版本线、草稿/过期、下一版本号、评测率、品类标签 |
| `web/src/views/*.vue` | 14 个视图：home / projects / plan / proto / build / verify / release / workers / agent / item / brd / notify / tech / config |
| `web/src/components/` | `SideBar`（单元 + 五阶段 + 通知）、`RightPane`（运行时事实流 + 接口契约）、`PageHeader` |

三条不变量（与 `docs/SPEC-frontend-wiring.md` 一致）：

1. **视图不持有真相。** 所有数据来自 `/api/*`；本地 `localStorage` 快照被显式删除（`i3:snap`）。
2. **写操作不做乐观更新。** 先发请求，成功后 `refresh()`；越权时界面必须原样不动。
3. **闸门只在服务端判。** `gates` 由 `internal/domain` 计算，前端只负责显示（`/api/state.gates`、`/api/agents[].gates`）。

切换交付单元会触发重拉：`App.vue` 里 `watch(() => S.pbcId)` → `refresh()`。少了它，页面会拿着上一个单元的账本渲染。

## 3. v2.3 新增接口

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/units` | 单元登记 + 各自 HEAD / tag / 提交数 / 工作项数；Agent 单元带 Agent 摘要 |
| `POST` | `/api/units` | body `{id,name,code,kind:"pbc"\|"agent"\|"hybrid",agentId}`；`agent`/`hybrid` 必须指向已存在的 Agent |
| `POST` | `/api/units/{id}/svcverify` | body `{pass,total,fails:[{n,c,ex}]}`；服务侧验收，供组合项合并判据 |
| `GET` | `/api/brd` | BRD 版本线（`vers[]`，最新在前，含草稿） |
| `POST` | `/api/brd` | body `{text,note}`；每次保存落一个新草稿版本（`v1.N`） |
| `GET` | `/api/agents` | 6 名数字员工 + 各自四闸 + 被哪些单元引用 |
| `GET` | `/api/agents/{id}` | 单个 Agent 的定义、工具白名单、评测、运行历史、用例 |
| `POST` | `/api/agents/{id}/eval` | body = 一次评测运行结果；**本服务不代跑评测**，执行器提交 |
| `POST` | `/api/agents/{id}/baseline` | body `{runId}`；把某次运行设为对比基线 |
| `POST` | `/api/agents/{id}/publish` | 四闸不全过则 403，并逐条给出未过的闸 |
| `POST` | `/api/agents/{id}/rollout` | body `{pct}`；只允许 10 → 50 → 100，不能跳档、不能回退 |
| `POST` | `/api/agents/{id}/rollback` | 流量归零并记录回滚目标版本 |
| `POST` | `/api/artifacts` | body `{artifact,text\|files,note}`；制品草稿入账（`prd/proto/ad/at/backlog` 存 md，`code` 存 `files`） |
| `GET` | `/api/facts` | 当前单元的原始事实流（seq/at/type/actor），新的在前 |

权限仍只由服务端签名 cookie 决定。制品草稿按制品定权：`prd`→BA、`backlog`/`at`→BA、`proto`/`ad`→PA、`code`→DA。

## 4. 三类交付单元的判据

| kind | 判据来源 | 能否发布 |
|---|---|---|
| `pbc` | `CheckCommit` 四闸：引用闭合 / 制品全部 accepted / 镜像与树一致 / 原型还原 | 四闸全过 |
| `agent` | `AgentGates` 四闸：上游闭合 / 工具白名单 / digest 一致 / 评测达标（通过率 ≥95%） | 四闸全过 |
| `hybrid` | PBC 前三闸 + `svc-verify`（服务验收全过）+ Agent 四闸，共 8 条 | 全部通过 |

前端不重复这套判定：`/api/state?pbc=...` 直接返回按 kind 组织好的 `gates`。
