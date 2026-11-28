# 导入原型评审 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal** 让团队导入原型文件，在原型页在线预览并记录画线批注。

**Architecture** 单元账本保存导入来源及内容摘要，并通过序号关联批注。Go 通过现有制品仓库读取原件。Vue 在图片、PDF 首页画布和固定布局的 HTML 沙箱上复用归一化画线。

**Tech Stack** Go、Vue、Vite、PDF.js、Playwright。

**Spec** `docs/superpowers/specs/2026-11-28-prototype-file-review-design.md`。

## Global Constraints

- PNG、JPEG、PDF、UTF-8 静态 HTML，单文件上限 20 MiB。
- PA 使用现有 `acceptProto` 权限导入和画线。只读评审文件独立于正式制品版本、审批和构建。
- PDF 首页可以在线预览。静态 HTML 预览允许文件内资源，隔离脚本和外部请求。

## Review Focus

- 同一摘要的两次导入分别持有自己的批注。
- 单元切换和浏览器刷新不会混用历史文件或批注。
- HTML 只能在只读沙箱中展示，并验证可见内容和样式。
- 错误格式、越权、超限、跨单元取文件及非法批注坐标会被拒绝。
- 图片和 PDF 的画线随图像尺寸按比例缩放，质量报告保留原有判定。

## 1. 导入和批注 API

文件范围为 `internal/artifacts/store.go`、`internal/store/{facts,project}.go` 和 `internal/bff/{server,v23,prototype_import,prototype_import_test}.go`。

- [ ] 增加 BFF 测试，覆盖无权限请求 403、文件超过 20 MiB 返回 413、PNG/JPEG/PDF/HTML 校验、单元隔离和导入事实落账次序。运行 `go test ./internal/bff ./internal/store ./internal/artifacts` 观察新增断言失败。
- [ ] 实现 `POST /api/prototype-imports?pbc=<id>`，multipart 文件字段 `file`。成功返回 201，投影收录在 `protoImports`，字段为 `seq,digest,fileName,mediaType,size,width,height,by,at`，后两个尺寸字段仅在服务端可验证时提供。
- [ ] 实现 `GET /api/prototype-imports/{seq}?pbc=<id>`，按当前单元投影取原件。HTML 响应附带沙箱和内容安全策略。路径只接收账本序号。
- [ ] 扩展 `POST /api/annotations?pbc=<id>`。导入批注使用 `{importSeq,line:{x1,y1,x2,y2},text}`，沿用 `annotation.added`，服务端检查存在的导入序号及 0–1 坐标。
- [ ] 重跑三个 Go 包测试，确认导入与旧事实回放兼容。

## 2. 在线预览和批注交互

文件范围为 `web/src/{api,store}.js`、`web/src/views/ProtoView.vue`、`web/src/components/ImportedPrototype.vue`、所需预览模块及 `web/package*.json`。

- [ ] 加入导入按钮、导入记录选择和「只读 · 导入」标识。PA 可执行写入。历史选择和写入请求绑定当前单元。
- [ ] 实现 PNG/JPEG、PDF 首屏画布和 HTML 沙箱预览。用固定 HTML 布局宽度和比例缩放定位覆盖层。PDF.js 按需加载，用首页 viewport 处理旋转和裁切。
- [ ] 在覆盖层支持拖线、输入和保存批注。保留起终方向，允许水平及竖直线。切换单元或文件时清空未保存画线，加载错误时保留清楚的提示。
- [ ] 用 `npm --prefix web ci` 和 `npm --prefix web run build` 验证依赖及产物。

## 3. 端到端验收和合并

文件范围为 `web/e2e/acceptance_proto_import.cjs`、用户权限表和自测报告所需证据。

- [ ] 用浏览器验收上传 PNG、JPEG、PDF、HTML、只读标识、PDF 首页画布及 HTML 内联样式。检查脚本和外部请求隔离。
- [ ] 验证 PNG 和 PDF 画线后的刷新、缩放、历史选择及 `annotation.added` 事实。用 API 检查 403、413、未知单元和质量闸原值。
- [ ] 在独立 Docker 环境跑新验收，并按 v32、exec、flow、full、v26 的顺序各跑一次现有回归。运行 `go vet ./...` 和 `go test ./...`。
- [ ] 记录验收及证据，审查合并树，提交中文 PR 关联 #62。验证目标分支及 CI 后合并到 `main`。
