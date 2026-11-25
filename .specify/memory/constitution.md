# i3-composer Constitution

i3 构建工作台：把一份 BRD 拆成交付单元，让每个单元的交付过程可回放、可核对、不掺假。

## Core Principles

### I. 事实日志是唯一权威（NON-NEGOTIABLE）

状态只能由 append-only 的事实重放得出；页面、接口、workflow 都不持有真相。

- 任何「看起来变了」的界面，背后必须有一条可查的事实（`item.created`、`test.run`、`publish.decided`、`project.joined`……）。
- 投影是纯函数：同样的账本重放出同样的视图；投影里不允许有随机数、当前时间、外部调用。
- 前端不做乐观更新：越权或失败时，账面状态不得先变再回滚。
- 账本正文（代码、文档）不通过列表接口外泄；列表只给标量结论。

### II. 判不了就说判不了（NON-NEGOTIABLE）

门禁要么有证据，要么如实标 Unknown。没有第三条路。

- 没有比对结果时，闸输出 `Unknown: true` 并写明「判不了」，绝不画绿勾。
- 不产出假数据：假部署地址、假 digest、假测试条数、假成员数一律禁止；没有进程就没有 URL。
- 演示数据必须可识别：种子实事的 `actor` 是 `seed`，不是人也不是流水线。

### III. 真实执行链，不做演示替身

`codegen → builder（真 go build / go test）→ artifacts（内容寻址）→ deployer（真进程 + /healthz）`。

- 每个结论都要能就地核对：测试几条过、digest 是什么、部署在哪。
- 做不到的环节要明说「未接入真实执行器，本轮不产出结论」，而不是编一个结果。
- Agent 单元的交付物是定义，没有独立进程，就不部署、不填 URL。

### IV. 隔离是默认值

`LEDGER_PREFIX` 隔离账本，`WORKFLOW_NAME` 隔离 workflow actor；实例号 = 前缀 + 单元 + 工作项。

- 两套账本、两套流程可以在同一套 Dapr 上并存而不互相抢工作项。
- 同一条 backlog 不允许并发跑：靠 Dapr 实例号 409 做锁，不自己造锁。
- 验收必须在干净账本上跑；被上一轮改过的账本（比如 Agent 编排被编辑）会让闸变红，那不是回归。

### V. 权限只认服务端会话（NON-NEGOTIABLE）

- 角色来自会话 cookie，请求体里的 `role` / `canApprove` 一律不读。
- 越权返回 403，且不得留下任何事实。

### VI. 注释写「为什么」，不写「是什么」

中文注释记录取舍、坑与判断依据：为什么用 `first-write` 而不是 `first-write-wins`、为什么应用必须监听 sidecar 的 app-port、为什么新项目 BRD 从 0.1 起步。复述代码的注释直接删掉。

### VII. 验收以账本事实为准

- 断言查事实流（`/api/items/{id}` 的 facts），不查 DOM 文案。
- 截图与事实同轮生成：截图里的 digest、部署地址、闸门结论必须和这一轮账本对得上。
- 「通过」只统计行为断言；外部未接入的能力（视觉比对、真实模型）单列，不混进通过数。

## 技术约束

- 后端：Go（标准库为主），Dapr sidecar（Redis statestore + pubsub，workflow 引擎），不引入额外框架。
- 前端：Vue 3.5 + Vite 6，构建产物 `web/dist` 由 Go BFF 托管（SPA 回落）。
- 运行面：`docker compose up -d`（placement / scheduler / daprd）+ 单二进制；daprd 的 `--app-port` 固定 8080，应用必须监听同一端口，否则 pubsub 事件收不到（启动会 WARN，`/api/health` 会带出 `pubsub.ok=false`）。
- 模型调用不在本仓库：Agent 的推理是可替换的规则实现，评测跑内置评测集。

## 开发流程

1. 起环境：`docker compose up -d`。
2. 构建产物跑服务：`go build -o /tmp/i3-composer-bin ./cmd/i3-composer`，再用环境变量启动——不要用 `go run`（它不向子进程转发 SIGTERM，部署起的进程会赖着不走）。
3. 工程闸：`go vet ./...`、`go test ./...`、`cd web && npm run build`。
4. 行为闸：`web/e2e/` 下的 Playwright 套件（v27 全链路 / exec / flow / v26 / full），一律用干净账本 + `BASE` / `RUN_TAG` / `SHOTS` 环境变量。
5. 改动涉及闸门、部署、账本时，自测报告要同轮更新，截图与事实一起进 `docs/selftest-*/`。

## Governance

- 本宪法优先于任何临时约定；与其冲突的实现要在 PR 里说明理由并先改宪法。
- 每个 PR 必须能跑通「开发流程」第 3、4 步；跑不通的不合。
- 新增「判不了」的能力时，只能新增 Unknown 分支，不能降级成通过。
- 修宪：版本号遵循语义化版本；MAJOR 表示原则增删或重定义，MINOR 表示新增约束，PATCH 表示措辞澄清。

**Version**: 1.0.0 | **Ratified**: 2026-11-25 | **Last Amended**: 2026-11-25
