# Implementation Plan: v3.2 核心交付链路

**Branch**: `feat/v32-core-chain` | **Spec**: [spec.md](./spec.md)

## Summary

把「单元五阶段 + 单元自己发布」改成「单元四阶段 + 打 tag 进池 + 项目级批次发布」。
技术上有三处关键移动：

1. 部署从单元 workflow 移到发布批次（BFF 调执行侧 `pipeline.Deploy`，回滚调 `Stop`）；
2. 打 tag 从 workflow 的人工事件改成 BFF 的显式动作（重新校验闸门后落 `tag.created`）；
3. 单元构成收敛为 pbc / agent，组合由批次的「一次勾选」表达。

## Technical Context

- 后端：Go；Dapr workflow（5 步 + verified 收口）；事实日志照旧（append-only，投影纯函数）。
- 新增账本：`releases`（批次与回滚）；单元账本新增 `item.verified`、`tag.created`（已有）。
- 前端：Vue 3.5 + Vite；新增 `OrchView.vue`，删除 `ReleaseView.vue`；侧栏四阶段 + 项目行编排图标。
- 执行侧：复用 `internal/pipeline`（Build/Test/Deploy/Stop），BFF 持有 `Pipe` 以在批次里部署与回滚。
- 种子：四单元（fee/qa/dun/dunx），基线测试与构建真跑（`seed.Options.Test`）。

## Constitution Check

| 原则 | 检查 |
|---|---|
| 事实日志唯一权威 | 打 tag、批次、回滚全部落事实；编排读接口只做投影 |
| 判不了就说判不了 | 视觉闸仍 Unknown；Agent 产物无进程则 URL 为空，不编地址 |
| 隔离 | 批次按 `proj` 过滤；实例号与账本前缀不变 |
| 权限只认会话 | 打 tag / 发布 / 回滚都走 `Require(ActTag)`；前端只做展示 |
| 注释写为什么 | 新增代码注释说明「为什么 tag 只认 HEAD」「为什么批次整批回滚」 |

## Project Structure

### Documentation

```
specs/001-v32-core-chain/
├── spec.md
├── plan.md
└── tasks.md
```

### Source Code

```
internal/store/facts.go        + item.verified / release.batch.created / release.batch.rolledback
internal/store/project.go      StepPlan 六步（tag 人工闸）、Item.Parent、verified/tagged 投影
internal/store/releases.go     ReleasesView 投影（新增）
internal/wf/delivery.go        五步 + verified，不再等待发布事件
internal/wf/activities.go      Verified activity（校验 HEAD digest 与测试）
internal/bff/release.go        打 tag / 编排读 / 批次发布 / 整批回滚（新增）
internal/bff/v23.go            createUnit 拒绝 hybrid；UnitProgress 增加 headTag
internal/bff/server.go         Task.Parent 透出；Pipe 字段；新路由
internal/seed/*               四单元 + 5 员工 / 2 产物 + Backlog 分层项 + 真实测试
web/src/views/OrchView.vue     发布编排页（新增）
web/src/views/VerifyView.vue   交付物卡（打 tag）
web/src/views/BuildView.vue    Backlog v3.2 重排
web/src/components/SideBar.vue 四阶段 + 项目行编排图标
web/src/views/WorkersView.vue  平台员工 / 交付产物分区
web/e2e/acceptance_v32.cjs     新全链路验收；其余四套改到新语义
```

## Complexity Tracking

- 部署与回滚横跨 BFF 与执行侧：`Server.Pipe` 注入同一份 `pipeline.Pipeline`，避免两套部署实现。
- 批次里 Agent 产物「发布」只是定义激活：事实里 `status=activated`、`url` 为空，读接口与前端都不补默认值。
- 就绪判定只用「HEAD 有 tag」：tag 本身是当时闸门与验收都过的凭据；不重复要求当下再跑一遍测试（否则会被别的流程改动牵连）。
