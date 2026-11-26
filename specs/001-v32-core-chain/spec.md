# Feature Specification: v3.2 核心交付链路（四阶段 / 打 tag / 发布编排）

**Feature Branch**: `feat/v32-core-chain`

**Created**: 2026-11-25

**Status**: Implemented（实现先于本规格落盘，本文件为回填；实现与规格已对齐）

**Input**: 原型 `i3-composer_交互原型_v3.2.html` 与 `原型说明_v2.8–v3.2.md`；
用户要求「按照新原型改动的实现」，范围确认为核心交付链路（运营 / 配置模块不在本次范围）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 单元只做到可以交付，人打 tag 进池 (Priority: P1)

交付负责人（DA）对一条工作项发起交付，机器跑完「生成制品 / 收敛 / 提交 / 测试 / 构建」五步后停下；SA 在验证页看完验收清单，按下「打 tag 固化交付物」，这个单元才进发布池。

**Why this priority**: 这是 v3.2 的语义地基——单元不再自己上环境；没有 tag 就没有发布资格。

**Independent Test**: 新建一个 PBC 单元 → 建工作项 → DA 发起 → 等到 `run=verified` → SA `POST /api/units/{id}/tag` → 单元 HEAD 上有 tag、编排页显示就绪。

**Acceptance Scenarios**:

1. **Given** 一个新建 PBC 单元，**When** DA 发起交付，**Then** 账本出现 `test.run`、`build.succeeded`、`item.verified`，且**没有** `deploy.succeeded`。
2. **Given** 单元停在待打 tag，**When** 验收清单没全过，**Then** 打 tag 返回 400 且点名「还差 N 条验收未通过」。
3. **Given** 单元已打 tag，**When** 重复打 tag，**Then** 返回 400「HEAD 已打过 tag」。

---

### User Story 2 - 项目级发布编排：批次锁 tag，整批回滚 (Priority: P1)

SA 在项目的发布编排页看到全部单元的就绪/阻塞，勾选要发的单元，选环境与方式，发布一个批次；批次锁住勾选单元的 tag 与 digest，PBC 真起进程并健康检查，Agent 产物无进程；任何问题只能整批回滚。

**Why this priority**: 「一起上、一起回」是组合需求的正确表达方式（组合不再是单元属性）。

**Independent Test**: 勾选一个已打 tag 的 PBC 与一个已打 tag 的 Agent → `POST /api/projects/{id}/orch/release` → 批次里 PBC 有 URL 且 `/healthz` 200、Agent `activated` 无 URL → `rollback` 后进程停止、重复回滚被拒。

**Acceptance Scenarios**:

1. **Given** 项目下 2 个单元已打 tag、2 个未打，**When** 打开编排页，**Then** 显示 2 就绪 / 2 阻塞，未打 tag 的进不了批次且被点名。
2. **Given** 一个混合批次，**When** 发布到 SIT，**Then** 批次记录锁住两个 tag 与 digest；PBC 部署有真实 URL，Agent 产物 URL 为空。
3. **Given** 一个已上线批次，**When** 整批回滚，**Then** 批次转 `rolledback`，部署进程停止；二次回滚 400。

---

### User Story 3 - 只保留两种交付单元 (Priority: P2)

PA 新建交付单元时只能选 PBC 或 Agent；不再有「PBC + Agent」组合单元。

**Why this priority**: 一个单元只有一个 tag；组合是发布时的选择，不是单元的属性。

**Independent Test**: `POST /api/units` 传 `kind=hybrid` → 400，错误信息说明拆分与批次的做法。

**Acceptance Scenarios**:

1. **Given** 新建单元表单，**When** 选择构成，**Then** 只有「微服务 PBC」「Agent（交付的产物）」两项。
2. **Given** 请求体里 `kind=hybrid`，**When** 创建，**Then** 被拒绝并给出「拆成两个单元、放进同一批次」的指引。

---

### User Story 4 - 构建页 Backlog 按 v3.2 重排 (Priority: P2)

打开 Backlog 先看到「这版要干哪些活」：版本用下拉、工作项在前并分层（story / task / 人负责的任务）、文档默认收起。

**Why this priority**: 文档是工作项的注脚，不该抢首屏；分层让 task 不再丢失、负责人不再串台。

**Independent Test**: 在构建页切到 Backlog → 下拉显示「1.0.1（当前）」；story 行加粗、task 用 └ 挂在下面；文档只留行数与「展开全文」。

**Acceptance Scenarios**:

1. **Given** 一个有 3 story + 若干 task 的单元，**When** 打开 Backlog，**Then** task 全部挂在对应 story 下，人负责的任务单独一段。
2. **Given** 负责人是 `x-ui`（原型工程师），**When** 渲染工作项表，**Then** 显示「X-原型工程师」，不回落成默认的全栈工程师。
3. **Given** 文档卡，**When** 首次打开，**Then** 默认收起，点「展开全文」才渲染 markdown。

---

### User Story 5 - Agent 工厂区分平台员工与交付产物 (Priority: P3)

Agent 工厂页上半是交付的 Agent（产物，可点开四阶段），下半是平台员工（生产者，只读、不单独打 tag）；命名统一「XX 工程师」。

**Why this priority**: 把产物写成员工会让「给测试工程师配版本号」这种概念错误出现在界面上。

**Independent Test**: 工厂页显示 5 名员工、2 个产物；全站不出现「质检工程师 / 催缴专员 / UI设计师 / 业务分析员」。

**Acceptance Scenarios**:

1. **Given** 种子数据，**When** 打开工厂页，**Then** 上半 2 个产物带「产物」徽章与所属单元，下半 5 名员工只读。
2. **Given** 任一页面，**When** 搜索旧命名，**Then** 无结果。

---

### Edge Cases

- 单元换端口运行（sidecar app-port=8080 之外）：工作流照跑但 pubsub 事件收不到；`/api/health.pubsub.ok=false` 会直接说出原因。
- 批次发布中途某个 PBC 部署失败：已起的进程全部收回，整批不落地（不产生批次事实）。
- 单元没有验收清单（新拆出来的）：打 tag 以机器测试与闸门为准，页面明确写出这一点。
- Agent 产物：测试是定义结构校验（真实检查），部署如实跳过，URL 为空。
- 已打 tag 的单元后来被别的流程改了 Agent 编排：编排页仍以「HEAD 有 tag」判就绪；闸门面板会显示 digest 不一致——两者都如实呈现，不互相掩盖。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 系统 MUST 只接受 `pbc` / `agent` 两种单元构成；`hybrid` MUST 被拒绝并给出拆分指引。
- **FR-002**: 交付 workflow MUST 执行五个机器步骤（生成制品、收敛、提交、测试、构建），完成后落 `item.verified`，且 MUST NOT 产生单元级部署事实。
- **FR-003**: 打 tag MUST 校验：HEAD 有 digest、HEAD 有全过的测试、闸门全过、（有清单时）验收全过；不满足时 MUST 返回点名原因的 400。
- **FR-004**: 打 tag MUST 只作用于 HEAD，已打过 MUST 拒绝；成功时落 `tag.created`（含 tag、commit、digest）。
- **FR-005**: 发布编排读接口 MUST 返回项目下单元的就绪状态、原因、HEAD tag 与 digest，以及历史批次。
- **FR-006**: 发布批次 MUST 校验：环境合法、至少一个单元、每个单元属于该项目且 HEAD 有 tag/digest；不满足 MUST 整批拒绝。
- **FR-007**: 批次 MUST 为每个 PBC 产物真起进程并等待 `/healthz` 200；Agent 产物 MUST 标 `activated` 且 URL 为空；任一部署失败 MUST 收回已起进程并整批失败。
- **FR-008**: 回滚 MUST 只能按批次进行，停止该批次所有部署进程，落 `release.batch.rolledback`；已回滚批次 MUST 拒绝再次回滚。
- **FR-009**: 前端侧栏 MUST 只有四个阶段；发布编排入口 MUST 在项目行，进入编排时 MUST NOT 展开单元阶段菜单或高亮单元。
- **FR-010**: 验证页 MUST 提供「打 tag 固化交付物」入口，并显示未满足的前置条件；打完后 MUST 显示「已进发布编排池」。
- **FR-011**: 构建页 Backlog MUST 使用版本下拉、工作项前置、story/task 分层、文档默认收起；task MUST 能挂到 story 下，负责人 MUST 显示真实 worker 名。
- **FR-012**: Agent 工厂 MUST 分开平台员工与交付产物；全站命名 MUST 统一「XX 工程师」（真人岗位「质检员（人工）」保留）。

### Key Entities

- **Unit（交付单元）**：`id`、`name`、`kind`（pbc/agent）、`agentId`（产物）、`proj`；HEAD 与 tag 由它自己的账本投影。
- **Item（工作项）**：`id`、`t`、`kind`（Story/Task）、`parent`、`owner`、`run`（idea/running/verified/tagged/blocked）。
- **Tag（tag.created）**：`tag`、`commit`、`digest`、`treeSha`、`by`；挂在 HEAD 提交上。
- **ReleaseBatch（release.batch.created）**：`id`（REL-YYYYMMDD-NN）、`proj`、`env`、`mode`、`units[]`（含锁定 tag/digest/url/status）、`by`、`at`、`status`。
- **ReleaseRollback（release.batch.rolledback）**：`id`、`by`、`at`、`note`。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 干净账本上五套验收 133 项全部通过（v32 37 / exec 25 / flow 19 / full 18 / v26 34）。
- **SC-002**: 一条新交付从发起到可打 tag 全程无需人工干预，账本里能查到测试条数、digest 与 `item.verified`。
- **SC-003**: 发布批次里 PBC 的部署地址 `/healthz` 返回 200；Agent 产物 URL 为空；回滚后同一地址不再 200。
- **SC-004**: 未打 tag 的单元 100% 被批次拒绝（error 点名单元名与原因）。
- **SC-005**: Backlog 页 3 个 story 与全部 task 正确分层，负责人显示真实 worker 名。
- **SC-006**: 全站不再出现旧命名与「PBC + Agent」构成。

## Assumptions

- 发布「环境」当前是标签 + 本地进程，没有真实多环境资源；灰度模式只记录在批次上（未做真实放量）。
- 视觉比对与真实模型推理未接入：相应闸门标「判不了」，评测跑内置评测集。
- 「把组合单元拆成两个」只有文案说明，没有拆分动作。
- 运营（门户/资源/监控/IAM）与配置（闸门表/参数）模块不在本次范围。
