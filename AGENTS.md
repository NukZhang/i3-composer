# i3 Composer 开发约定

i3 Composer 把项目 BRD 拆成交付单元。PBC 和 Agent 单元经历方案、原型、构建、验证四阶段，打 tag 后进入项目级发布编排。开发与验收遵循 [项目宪法](.specify/memory/constitution.md)。

## 项目地图

- `cmd/i3-composer` 装配服务。`internal/bff` 管 HTTP、会话权限、SSE 和前端托管。工作空间与项目见 `projects.go`，单元、BRD 和门禁见 `v23.go`，项目级发布见 `release.go`。
- `internal/domain` 管领域规则，`internal/store` 管追加事实和确定性投影，`internal/wf` 管 Dapr Workflow 与 Activities。`components/` 配置 Redis 状态存储与 Pub/Sub。
- `internal/pipeline` 串联制品生成、真实 Go 编译与测试、内容寻址和进程健康检查。`internal/agentexec` 执行本地 Agent 规则，模型网关在仓库外。
- `web/src` 使用 Vue 与 Vite，BFF 托管 `web/dist`。执行能力见 [执行说明](docs/EXECUTION.md)，行为验收见 [v3.2 自测](docs/selftest-v32/自测报告.md)。

## 不变量

- 业务写入通过 `Store.Append` 或 `AppendOp` 追加事实，保留账本重放兼容性、ETag CAS、哈希链与落链后的通知。列表提供摘要，正文使用专用读取接口。演示种子保留 `actor=seed`。
- Workflow 保持确定性，网络、时钟和副作用放进 Activity。用 `LEDGER_PREFIX` 隔离账本，用 `WORKFLOW_NAME` 隔离工作流。实例 ID 由前缀、单元与工作项组成，依靠 Dapr 的实例冲突保护避免重复发起。
- 角色来自服务端会话 Cookie，越权返回 403 且账本留空。Vue 写操作走 API，成功后刷新服务端投影，SSE 通知触发刷新。
- 单元的机器五步完成后等待打 tag。项目级发布按 tag 创建批次，批次锁定 tag 与 digest，回滚按批次执行。
- 缺少比对证据时输出 `Unknown`，页面显示「判不了」。测试条数、digest 和部署 URL 必须有真实执行证据。只有定义制品的 Agent 省略独立进程与部署 URL。
- 中文注释记录判断依据、取舍与兼容边界。提交和 PR 描述用中文交代改动及理由，提交元数据仅含项目要求项，省略代理署名和工具尾注。

## 本地运行与验证

- 工作区存在 `../docker-local/compose.yml` 时，沿用该配置运行容器。现有实例通过附加 Compose 文件启动时，使用同一组配置文件。保留 Redis 与制品卷，写入业务数据的验收使用独立 Compose 项目。
- 仓库自带 Compose 用于启动 Dapr，Redis 复用宿主机 6379。用 `go build -o /tmp/i3-composer-bin ./cmd/i3-composer` 构建后直接运行二进制，以便 SIGTERM 触发子进程回收。应用端口须与 daprd 的 `--app-port` 一致，默认 8080。用 `/api/health` 的 `pubsub.ok` 验证接线。
- 工程验证运行 `go vet ./...`、`go test ./...`、`npm --prefix web ci` 和 `npm --prefix web run build`。
- Playwright 套件在 `web/e2e/`。在干净账本上按 `acceptance_v32.cjs`、`acceptance_exec.cjs`、`acceptance_flow.cjs`、`acceptance_full.cjs`、`acceptance_v26.cjs` 的顺序执行。v32 依赖初始种子单元数，v26 会改动 Agent 编排与评测。使用 `BASE`、`RUN_TAG`、`SHOTS` 配置目标与输出，容器内用可用的 Chromium 路径运行。
- 业务行为核对事实流，界面行为核对 DOM。改动门禁、部署或账本语义时，同轮更新自测报告、截图和事实证据。外部能力的接入状态单列。

## Spec Kit

从本仓库根目录运行 `.specify/` 脚本。规格、计划与任务存放在同一个 `specs/NNN-name/` 目录。模板在 `.specify/templates/`，当前规格路径见 `.specify/feature.json`。

- Codex 从 `.agents/skills/` 发现技能。对话中用 `$speckit-specify` 写规格，必要时用 `$speckit-clarify` 澄清，再用 `$speckit-plan`、`$speckit-tasks` 和 `$speckit-implement` 推进。
- `$speckit-analyze` 检查规格、计划与任务的一致性，`$speckit-checklist` 检查需求质量，`$speckit-converge` 对照现有实现补齐任务，`$speckit-taskstoissues` 将任务转为 GitHub issues。原则变更使用 `$speckit-constitution`。
- Claude Code 使用 `.claude/skills/` 中的对应技能和 `/speckit-…` 调用形式。共享脚本、模板和现有规格供两种客户端使用。共享脚本提示 `/speckit-…` 时，在 Codex 对话中使用对应的 `$speckit-…`。
