# CLAUDE.md

i3 构建工作台：一份 BRD → 拆成交付单元（PBC / Agent）→ 每个单元跑完「方案 / 原型 / 构建 / 验证」四阶段并打 tag → 项目级发布编排按批次上环境。核心不是把页面画出来，而是让交付过程**可回放、可核对、不掺假**。

项目宪法在 `.specify/memory/constitution.md`，冲突时以它为准。

## 架构（从下往上）

| 层 | 位置 | 职责 |
|---|---|---|
| L1 模型网关 | 不在本仓库 | Agent 推理是可替换的规则实现 |
| L2 Dapr | `docker-compose.yml`、`components/` | workflow 引擎、statestore、pubsub（Redis） |
| L3 领域 | `internal/domain`（纯判断）、`internal/store`（事实日志，唯一权威）、`internal/wf`（workflow 编排） | 账本 + 投影 + 编排 |
| L4 BFF | `internal/bff`、`cmd/i3-composer` | `/api`、会话权限、SSE、静态托管 `web/dist` |
| 执行侧 | `internal/codegen` / `builder` / `artifacts` / `deployer` / `pipeline` | 真生成、真 `go test`/`go build`、内容寻址、真进程 + `/healthz` |
| 前端 | `web/src`（Vue 3.5 + Vite 6） | 只读投影，不持真相 |

关键目录：`internal/bff/projects.go`（v2.7 工作空间 / 项目）、`internal/bff/v23.go`（单元 / BRD / 门禁）、`internal/wf/activities.go`（六步 + 发布闸）、`web/e2e/`（验收套件）。

## 不变量（改代码前先看）

1. **事实日志是唯一权威**：任何状态变化必须先落事实；投影是纯函数；前端不做乐观更新。
2. **判不了就说判不了**：没有证据的闸输出 `Unknown`，不画绿勾；不产出假 URL / 假 digest / 假测试条数。
3. **隔离**：`LEDGER_PREFIX` 隔离账本，`WORKFLOW_NAME` 隔离 workflow；实例号 = 前缀 + 单元 + 工作项；重复发起靠 Dapr 409。
4. **单元只做到可以交付**：机器五步（制品/收敛/提交/测试/构建）跑完停在「打 tag」，部署不在单元里；tag 是发布编排唯一认的东西，批次锁 tag 与 digest、只能整批回滚。
5. **权限只认会话**：请求体里的 `role` 不读；越权 403 且不留事实。
6. **注释写「为什么」**（中文），不写「是什么」。

## 起服务

```bash
docker compose up -d                      # placement + scheduler + daprd（Redis 复用宿主机 6379）

go build -o /tmp/i3-composer-bin ./cmd/i3-composer
ADDR=:8080 LEDGER_PREFIX=demo/ WORKFLOW_NAME=i3-delivery \
  SEED_DEMO=1 WORK_DIR=/tmp/i3-work ARTIFACT_DIR=/tmp/i3-artifacts \
  /tmp/i3-composer-bin
```

- **不要用 `go run`**：它不向编译出的子进程转发 SIGTERM，部署起的进程会残留。
- **必须监听 8080**：daprd 的 `--app-port=8080` 固定，应用换端口时 pubsub 事件收不到（启动会 WARN，`/api/health` 的 `pubsub.ok=false`）。
- 环境变量：`ADDR`、`DAPR_HTTP_ENDPOINT`(3500)、`DAPR_GRPC_ENDPOINT`(50001)、`STATE_STORE`、`PUBSUB`、`WORKFLOW_COMPONENT`、`WORKFLOW_NAME`、`LEDGER_PREFIX`、`WORK_DIR`、`ARTIFACT_DIR`、`SEED_DEMO`、`SESSION_KEY`。

## 验证

```bash
go vet ./... && go test ./...
cd web && npm run build

# 行为验收（Playwright；仓库根的端口按需改）
cd web/e2e
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_v32.cjs   # v3.2 全链路（四阶段/tag/批次/回滚）
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_exec.cjs   # 执行链真证据 + 批次部署 + pubsub
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_flow.cjs   # 写路径与权限（发起 / 打 tag）
BASE=http://127.0.0.1:8080 node acceptance_v26.cjs                 # 工具体验回归
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_full.cjs   # 从零建单元全流程
```

验收约定：

- **用干净账本**（新 `LEDGER_PREFIX` + 新 `WORK_DIR`）。同一账本跑第二轮会重名重号，断言会乱。
- 断言查事实流（`/api/items/{id}` 的 `facts`），不查 DOM 文案。
- 截图与事实同轮生成；`SHOTS`、`RUN_TAG` 可覆盖输出目录与命名。
- `acceptance_v26` 会真正编辑 Agent 编排并重建，跑完后该账本里 Agent 单元的 digest/评测闸会变红——这是真实反应，不是回归；所以 v32 的截图必须在 v26 之前、同一轮里截。
- 各套件都会建自己的单元；`acceptance_v32` 断言「项目下 4 个种子单元」，所以它必须跑在别的套件之前（干净账本第一棒）。
- 依赖：`NODE_PATH=<有 playwright 的 node_modules>`，Chrome 用 ms-playwright 缓存里的 Chromium（见脚本顶部 `CHROME`）。

## 文档

- `README.md`：项目门面与快速开始
- `docs/whitepaper.md`：白皮书（问题 / 模型 / 架构 / 商业模式 / 边界）
- `docs/EXECUTION.md`：执行链哪些是真、哪些没接
- `docs/FRONTEND.md`、`docs/SPEC-frontend-wiring.md`：前后端接线与排障
- `docs/prototypes/`：v2.x 交互原型
- `docs/selftest-v27/自测报告.md`：v2.7 全链路自测（含截图与问题清单）
- `docs/selftest-v32/自测报告.md`：v3.2 全链路自测（四阶段 / 打 tag / 发布批次）

## 规格驱动开发（spec-kit）

本仓库已安装 spec-kit（`.specify/` + `.claude/skills/speckit-*`，skills 模式），流程：

`/speckit-specify`（写 `specs/NNN-name/spec.md`）→ `/speckit-clarify` → `/speckit-plan` → `/speckit-tasks` → `/speckit-implement`；另有 `/speckit-analyze`、`/speckit-checklist`、`/speckit-converge`、`/speckit-taskstoissues`；原则变更走 `/speckit-constitution`。

- 规格落在 `specs/`，实现落在 `internal/`、`web/src/`。
- 宪法里「判不了就说判不了」「不做假数据」是硬约束，规格与任务拆解不得绕开。

## 提交约定

- 提交信息与 PR 描述用中文，说明「改了什么、为什么」。
- 不加署名/工具尾巴。
- 改闸门、部署、账本语义时，同轮更新自测报告与截图。
