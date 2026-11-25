# 前端：Vue 3 工作台（原型 v2.6）

> 旧方案（让 `web/index.html` 这份原型副本 + `web/adapter.js` 改 DOM）已废弃并归档到
> `docs/prototypes/i3-composer_v2.1-wired.html`。现在的前端是 `web/` 下的 Vue 单页应用，
> 界面与交互以 `docs/prototypes/i3-composer_v2.6.html` 为准。

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

验收脚本（Playwright，需本地有 playwright）：

```bash
cd web/e2e
BASE=http://127.0.0.1:8080 node acceptance_v26.cjs   # v2.6 的编排/验收/质量报告
BASE=http://127.0.0.1:8080 node acceptance_flow.cjs  # 写路径：建任务 → 发起 → 拍板 → 发布
```

## 2. 目录

| 路径 | 作用 |
|---|---|
| `web/src/main.js` | 挂载 `App.vue`，引入 `styles/app.css`（样式逐条搬自原型 `<style>`） |
| `web/src/App.vue` | 顶栏（角色切换 / 主题 / Agent 工厂入口）、侧栏、舞台、右栏、toast；`onMounted` 拉全量 + 连 SSE |
| `web/src/store.js` | 唯一的响应式状态 `S`：`view / pbcId / agentId / role / state / units / brd / agents / facts …`，`refresh()` 串行重拉，`connect()` 订阅 SSE |
| `web/src/api.js` | 与 BFF 的全部读写；`stream()` 只通知「有变化」，不携带账本内容 |
| `web/src/derive.js` | 纯派生：版本线、草稿/过期、下一版本号、评测率、节点类型、工具接线 |
| `web/src/views/*.vue` | 14 个视图：home / projects / plan / proto / build / verify / release / workers / agent / item / brd / notify / tech / config |
| `web/src/components/` | `SideBar`（单元 + 五阶段 + 通知）、`RightPane`（运行时事实流 + 接口契约）、`PageHeader`、`AgentStudio`（编排工作台）、`EvalWorkbench`（评测台）、`KbAccuracy`（知识库准确度）、`QualityReport`（机器自检）、`AcceptTerminal`（真机操作 / 对话验收）、`ProtoAgentReview`（质检复核屏）、`ProtoDunConsole`（催缴业务台） |

三条不变量（与 `docs/SPEC-frontend-wiring.md` 一致）：

1. **视图不持有真相。** 所有数据来自 `/api/*`；本地 `localStorage` 快照被显式删除（`i3:snap`）。
2. **写操作不做乐观更新。** 先发请求，成功后 `refresh()`；越权时界面必须原样不动。
3. **闸门只在服务端判。** `gates` 由 `internal/domain` 计算，前端只负责显示（`/api/state.gates`、`/api/agents[].gates`）。

切换交付单元会触发重拉：`App.vue` 里 `watch(() => S.pbcId)` → `refresh()`。少了它，页面会拿着上一个单元的账本渲染。

## 3. v2.6 的两个新页面

### 3.1 构建页 → Agent 编排工作台（`AgentStudio.vue`）

Agent 的构建产物不是一份 YAML，是**一张编排**：左边节点清单，中间画布，下面左边节点配置、右边试跑。

- 七种节点各自配各自的东西：开始（输入变量）/ 工具（入参映射）/ 知识库（库与版本）/ 模型（提示词、温度）/ 分支（条件表达式）/ 人工（等谁、超时）/ 结束（输出字段）。
- **工具权限表多一列「用在哪」**：从节点配置反查接线；白名单给了但流程里没接线的显示红字。
- **知识库依赖**：库的文档数 / 版本 / 同步方式 / 归属 + topK / 阈值 / 重排；库版本进 digest。
- **试跑这一条**：输入是真实样例，输出带逐节点耗时链；跑过的这条可「沉淀为评测用例」进验证阶段的评测集。
- **保存编排 → digest 变 → 构建 → 换版本号**：改了没构建，digest 闸立刻红，评测也要重跑。

### 3.2 验证页 → 真人验收台（`VerifyView.vue`）

验证不是看报告，是人自己上手跑一遍。

- 六条 AC 一排 chip，点一条切换当前验收对象；右侧是 Given / When / Then 与「通过 / 不提过」。
- PBC 是真机操作（筛账期、勾选、批量生成），Agent 是对话验收（「你来当那个客户」，真发一句）。
- 「不提过」展开 bug 表单：标题预填、**复现步骤自动带入刚才的操作**；提交后 bug 落 `defect.created` 事实并进 backlog。
- 机器自检（四闸 / 评测台 / 知识库准确度）挪到构建页的「质量报告」——那是构建结果的查看，不算验收。

## 4. v2.6 新增接口

| 方法 | 路径 | 说明 |
|---|---|---|
| `PUT` | `/api/agents/{id}/flow` | body `{flow}`；保存编排。digest 立即变，但没构建前闸门是红的 |
| `POST` | `/api/agents/{id}/build` | 构建：算 digest、换版本号；没改就返回 `changed:false`（不制造空版本） |
| `POST` | `/api/agents/{id}/trial` | 记录一次试跑：执行器按编排图真跑一遍（标 `source=executor`），输入变了结论就变 |
| `POST` | `/api/agents/{id}/cases` | 把跑过的一条沉淀为评测用例（用例进评测集，digest 随之变化） |
| `POST` | `/api/agents/{id}/kb/{idx}/dispatch` | 把知识库问题单派给库的 owner |
| `POST` | `/api/checks/{id}/decide` | body `{st:"pass"\|"fail",title,sev,steps}`；不通过则同时产生 bug 与 backlog 修复项 |
| `POST` | `/api/units/{id}/chat` | 验收对话；回复来自执行器真跑一遍编排（标 `source=executor`） |

权限仍只由服务端签名 cookie 决定。编排 / 构建 / 试跑是 `build`（DA）；验收判定与知识库问题单是 `acceptAT` / `brd`（BA）。

## 5. 三类交付单元的判据

| kind | 判据来源 | 能否发布 |
|---|---|---|
| `pbc` | `CheckCommit` 四闸：引用闭合 / 制品全部 accepted / 镜像与树一致 / 原型还原 | 四闸全过 |
| `agent` | `AgentGates` 四闸：上游闭合 / 工具白名单 / digest 一致 / 评测达标（通过率 ≥95%） | 四闸全过 |
| `hybrid` | PBC 前三闸 + `svc-verify`（服务验收全过）+ Agent 四闸，共 8 条 | 全部通过 |

前端不重复这套判定：`/api/state?pbc=...` 直接返回按 kind 组织好的 `gates`。

两条 v2.6 才有的硬规矩（都在服务端判）：

- **digest 一致** = 当前定义算出的 digest 与最后一次构建落下的 digest 相等。改了编排没构建 → 闸红。
- **评测针对旧版本** = 最近一次评测跑在旧 digest 上 → 评测闸红，旧分数不能给新版本背书。

## 6. 执行侧是真的

构建 / 测试 / 部署 / Agent 试跑 / pubsub 收发这五处都换成了真实现，
逐条证据见 [EXECUTION.md](EXECUTION.md)。页面上的「5/5 通过」来自 `go test` 的输出，
digest 是编译产物的字节 sha256，部署地址上的 `/healthz` 真的返回 200。

## 7. 验收脚本

```bash
cd web/e2e
BASE=http://127.0.0.1:8080 node acceptance_exec.cjs   # 执行侧：真测试 / 真构建 / 真部署 / 真收发
BASE=http://127.0.0.1:8080 node acceptance_flow.cjs  # 写路径：建任务 → 发起 → 拍板 → 发布
BASE=http://127.0.0.1:8080 node acceptance_v26.cjs   # 界面：编排工作台 / 质量报告 / 验收台
BASE=http://127.0.0.1:8080 node acceptance_full.cjs  # 从零：建单元 → 真跑六步 → 发布（放最后跑）
```

`acceptance_full.cjs` 会自己建交付单元，先跑它会让 `acceptance_v26.cjs`
里「种子单元只剩三个」那条断言看到四个单元——所以它放最后。
