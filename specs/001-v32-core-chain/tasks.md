# Tasks: v3.2 核心交付链路

**Input**: [spec.md](./spec.md) / [plan.md](./plan.md)
**Status**: 全部完成（实现先于规格落盘；本文件为回填，勾选项均已在账本与验收里验证）

## Phase 1: 账本模型

- [x] T001 `internal/store/facts.go`：新增 `item.verified`、`release.batch.created`、`release.batch.rolledback`
- [x] T002 `internal/store/project.go`：`StepPlan` 改为六步（第五步是人工闸「打 tag 进池」）；`Item` 增加 `Parent`
- [x] T003 `internal/store/project.go`：投影支持 `run=verified` / `run=tagged`；tag 落到对应工作项
- [x] T004 `internal/store/releases.go`：`ReleasesView` 纯函数投影（批次新的在前、回滚改状态、空账本给空数组）
- [x] T005 `internal/store/releases_test.go`：批次顺序、回滚状态、Agent 无 URL、空账本不返回 null

## Phase 2: 编排与执行

- [x] T006 `internal/wf/delivery.go`：五步 + verified 收口，删除等待发布事件与单元级部署
- [x] T007 `internal/wf/activities.go`：新增 `Verified`，校验 HEAD digest 与「测试针对 HEAD 且全过」
- [x] T008 `internal/wf/types.go` / `delivery.go`：注册 `ActVerified`
- [x] T009 `internal/pipeline`：沿用 Build/Test/Deploy/Stop，不新增实现

## Phase 3: BFF

- [x] T010 `internal/bff/release.go`：`POST /api/units/{id}/tag`（测试 / 闸门 / 验收清单三层前置，只认 HEAD）
- [x] T011 `internal/bff/release.go`：`GET /api/projects/{id}/orch`（就绪判定、原因、tag/digest、历史批次）
- [x] T012 `internal/bff/release.go`：`POST .../orch/release`（整批校验 → 真部署 → 失败全收 → 落批次事实）
- [x] T013 `internal/bff/release.go`：`POST .../orch/rollback`（整批停进程、落回滚事实、拒重复回滚）
- [x] T014 `internal/bff/v23.go`：`createUnit` 拒绝 hybrid 并给拆分指引；`UnitProgress` 增加 `headTag`
- [x] T015 `internal/bff/server.go`：路由注册；`Task` 透出 `Parent`；`Pipe` 字段注入
- [x] T016 `cmd/i3-composer/main.go`：`srv.Pipe = exec`；种子注入 `Test` 回调

## Phase 4: 前端

- [x] T017 `web/src/views/OrchView.vue`：编排链 / 筛选 / 勾选 / 就绪检查 / 预览锁 tag / 历史批次 / 整批回滚
- [x] T018 `web/src/components/SideBar.vue`：四阶段；项目行编排图标（灰/黄/绿）；编排页不展开阶段、不高亮单元
- [x] T019 `web/src/views/VerifyView.vue`：交付物卡（前置条件、打 tag、已进池）；卡片移出「有清单」条件
- [x] T020 `web/src/views/BuildView.vue`：Backlog 版本下拉 / 工作项前置 / story-task 分层 / 文档收起
- [x] T021 `web/src/views/WorkersView.vue`：平台员工与交付产物分两区
- [x] T022 `web/src/views/ProjectsView.vue`：只保留两种构成；`run` 文案补 verified/tagged
- [x] T023 `web/src/api.js` / `store.js` / `App.vue` / `RightPane.vue`：新接口、`tick` 重拉、`orch` 路由、右栏接口表
- [x] T024 删除 `web/src/views/ReleaseView.vue`；`PlanView` 的「去发布页」改为「去发布编排」

## Phase 5: 种子

- [x] T025 `internal/seed/seed.go`：四单元（fee/qa/dun/dunx）；基线 tag 按单元现场给
- [x] T026 `internal/seed/seed.go`：`Options.Test`，基线真跑测试并落 `test.run`
- [x] T027 `internal/seed/agents.json`：5 员工 + 2 产物（`qa-agent` / `dun-agent`），命名统一「XX 工程师」
- [x] T028 `internal/seed/checks.json`：新增 `dunx` 的 5 条验收（EV-1 失败）

## Phase 6: 验收

- [x] T029 `web/e2e/acceptance_v32.cjs`：37 项（结构 / 编排 / 批次 / 回滚 / 打 tag 前置 / 新单元全链路 / Backlog / 工厂 / 命名）
- [x] T030 `acceptance_exec.cjs`：改到「单元不部署 + 批次部署 + 回滚收进程」
- [x] T031 `acceptance_flow.cjs`：改到「发起 → verified → DA 打 tag 403 → SA 打 tag 成功」
- [x] T032 `acceptance_full.cjs`：改到「五步 → 打 tag → 批次 → 健康检查」
- [x] T033 `acceptance_v26.cjs`：四单元两构成、产物 id、工厂 2+5；删除旧名断言
- [x] T034 删除 `acceptance_v27.cjs`（被 v32 取代）
- [x] T035 `docs/selftest-v32/自测报告.md`：133/133 + 7 张截图 + 问题清单
- [x] T036 `CLAUDE.md` / `docs/EXECUTION.md`：同步四阶段、批次发布、验收顺序

## Dependencies & Execution Order

1. Phase 1（账本模型）→ 2（编排）→ 3（BFF）→ 4（前端）：后者依赖前者的投影字段与新接口。
2. Phase 5（种子）可与 3、4 并行；Phase 6 必须在 1–5 之后，且 `acceptance_v32` 是第一棒。

## Parallel Opportunities

- 前端各视图（T017–T024）互不依赖，可并行改。
- 四套回归验收（T030–T033）互不共享状态，可并行跑在不同账本前缀上。

## Verification

干净账本 `LEDGER_PREFIX=v32i/`、`:8080` 上依次跑：

| 套件 | 结果 |
|---|---|
| acceptance_v32 | 37/37 |
| acceptance_exec | 25/25 |
| acceptance_flow | 19/19 |
| acceptance_full | 18/18 |
| acceptance_v26 | 34/34 |
| go vet / go test / npm run build | 全绿 |
