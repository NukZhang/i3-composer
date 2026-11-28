# 执行侧：哪些是真的，哪些还依赖外部

> 这份文档回答一个问题：**跑起来的东西，有多少是自己算出来的，有多少还是外面借来的。**
>
> 判据只有一条：**看证据。** 页面上写「5/5 通过」不算数，账本里那条
> `test.run` 事实里写着 `pass:5, total:5`，而 `go test` 就在这台机器上跑过，
> 才算真。

---

## 一、逐条对照

| 环节 | 状态 | 证据在哪 |
|---|---|---|
| workflow 编排（派活、重试、超时） | ✅ 真 | Dapr durabletask + gRPC work-item 流；历史落在 Redis `dapr.internal.default.i3-composer.workflow\|\|<实例>` |
| 事实日志（哈希链 + etag CAS） | ✅ 真 | Redis `log:<pbc>:<seq>:<hash>`；`store.Load` 会重算哈希链，篡改即报错 |
| 权限闸 | ✅ 真 | HMAC 签名 cookie（`i3_sess`）；越权返回 403，界面不假装成功 |
| 制品生成 · 机制 | ✅ 真 | `internal/codegen` 真的落出文件：markdown 正文 + 可编译的 Go 模块（`go.mod` / `main.go` / `main_test.go`），内容进制品树、算进 digest |
| 制品生成 · 内容 | ❌ 未接 | 内容是**按标题选术语 + 填骨架**，不是按这个单元的 spec 实现（#90）。边界见第三节 |
| 测试 · 机制 | ✅ 真 | `go test -count=1 -v ./...` 的真实输出；不过就阻断链路（`test.run` 事实带 `failures`） |
| 测试 · 是否对着需求 | ❌ 未接 | 验的是骨架自带的五条约定（健康 / 列表 / 幂等 / 统计一致 / 错误可读），不是 spec 里的业务口径（#90/#93）。「5/5 恒绿」说明骨架没坏，不说明这个单元被验过 |
| 构建 | ✅ 真 | `go build -trimpath` 出二进制；digest = **二进制字节的 sha256**（内容寻址），不是树摘要拼的 |
| 部署（发布批次） | ✅ 真 | v3.2 起单元自己不再部署：打 tag 进池后，由项目级批次发布起构建产物为进程，等 `GET /healthz` 返回 200 才记 `deploy.succeeded`；批次只能整批回滚，回滚时把进程一并收掉 |
| Agent 试跑 / 对话验收 | ✅ 真（本地判定） | `internal/agentexec` 按编排图逐节点执行：读会话、检索知识库、跑合规规则、判分支、转人工；**输入变了结论就变** |
| pub/sub | ✅ 真（收发闭环） | 声明式订阅 `components/subscription.yaml` → sidecar 建 consumer group → 事件回到 `/api/events/{topic}` → 落 `event.received` 事实；workflow 的**每一步**都发 `item.changed` |
| 种子数据 | ⚠️ 演示数据（已标注） | actor 写作 `seed`，页面上标「演示数据」；真跑出来的事实 actor 是 `x-fe`/`x-qa`/`x-ops`/`SA` |
| 仓库自己的 CI | ✅ 已接 | `.github/workflows/ci.yml`：每个 PR 跑 gofmt / go vet / go build / go test -count=1 + 前端 npm ci / build |
| 交付构建走 CI 服务 | ❌ 未接 | 产品里的构建是**真的编译**（`internal/pipeline` → `go build`），但跑在本机而不是 CI；接上要换掉 `pipeline.Build` 的实现，上层契约不变 |
| 真模型调用 | ❌ 未接 | Agent 的判定由**本地规则引擎**算（合规规则库里的 R-03/R-05/R-07/R-11），不是模型推理 |
| 真实业务数据 | ❌ 未接 | 工具读的是仓内的语料 `internal/agentexec/data/corpus.json`，不是真实会话/账单系统 |

---

## 二、几个容易被当成「真」但不是的东西

**1. digest 曾经是拼出来的**

以前 `mockBuilder` 返回 `sha256:TreeSHA-...`——它只是树摘要换个前缀，
既不是编译产物的摘要，也不能用来核对「线上跑的就是这份字节」。
现在 digest 是 `go build` 出来的二进制的 sha256，可以随时复算：

```bash
sha256sum .i3-artifacts/blobs/sha256/<hex>   # 与事实里的 digest 一致
```

**2. 测试条数曾经是编出来的**

以前 `Test` activity 返回 `6 + len(tree)`——树里有几件制品就算几条测试。
现在条数来自 `go test` 的输出，失败时 `test.run` 事实里带失败用例名，
并阻断整条链路。

**3. 部署地址曾经是拼出来的**

以前 `Deploy` 拼一个 `https://test.i3.internal/fee-<hex>` 就当成功。
现在地址来自真实监听的端口，`/healthz` 返回 200 才算数；
`/api/<资源名>` 返回的是这个进程内存里的真实数据（资源名按工作项标题选，见第三节）。

**4. Agent 试跑曾经是回放**

以前把定义里存好的样例原样端出来，事实里明写 `source:"sample"`。
那证明不了任何东西：输入变了输出也不会变。
现在按编排图真跑一遍，结论随输入变；样例只作为「没接执行器时的兜底」，
且在界面上标出来。

**5. 「生成了代码」曾经被当成「按需求生成了代码」**

生成器确实落出了文件、确实能编译能测试——这部分一直是真的。但它产出的内容
曾经是一份写死的缴费模板做占位符替换（#93 之后去掉了写死的业务口径，
改成按标题选术语 + 填骨架）。所以「制品生成 ✅」只能读成「机制真」，
不能读成「按这个单元的规格实现了」。两者之间的差就是 #90（spec 环节）
与 #91/#92/#94（语言、架构、增量）。第三节把边界逐条写清楚。

**6. pub/sub 曾经只发不收，而且 workflow 的推进绕过了它**

事件发进 Redis stream 就没人消费了——看起来像消息总线在工作，其实是单向的。
更糟的是 workflow 推进走的是 `log.Notify`（事实一落就直接推 SSE），
中间那层总线成了装饰品：谁都能看见 `tag.created` 进了 Redis stream，
但没有任何东西消费它，也没有任何一步真的经过它。

现在两端都接上了：

- 收：`components/subscription.yaml` 让 sidecar 建 consumer group，
  事件回到 BFF 落成 `event.received` 事实，页面上能看到「收到的事件」；
- 发：**workflow 的每一步**（生成制品、收敛、提交、测试、构建，以及 verified 收口）
  都发一条 `item.changed`，不是只有后面几步发；测试与构建还会额外发一条细粒度事件。
  一次交付在总线上能数出 8 条 `item.changed`（5 步 + verified + 测试/构建各一条）。

怎么核对「总线不是装饰品」：跑完一条交付后数收到的 `item.changed`，
条数必须盖得住步骤数。`acceptance_exec.cjs` 里那条
「workflow 每一步都经总线发过事件」就是干这个的。

**7. 种子曾经冒充历史**

种子写的版本号、评测分、发布记录看起来像真干过。现在它们的 actor 是 `seed`，
界面上标「演示数据」；真跑出来的事实 actor 是具体角色，一眼能分开。

**8. 部署成功过，进程却赖着不走**

部署是起真进程，那就得有人收。早先的版本只在「换版本 / 回滚」时停旧进程：
每跑一轮验收就在机器上多留一个进程，服务退出时更是一个都不收
（实测一轮下来攒了 11 个）。现在：

- 同 (pbc, env) 换版本先停旧的；
- 服务退出时 `StopAll()` 把起过的进程全部收掉；
- 起进程失败/健康检查超时的路径也 Kill + Wait，不留僵尸；
- `Alive` 的判据是「reaper 收完了没有」——`ProcessState` 只有 `Wait` 之
  后才填，而僵尸进程对信号 0 也照样应答，两者都判不出真死。

判据很简单：验收跑完，`ps aux | grep app.bin` 只该看到当前在用的那几个部署；
停掉服务，一个都不该剩。

**9. 「没比对过」曾经被显示成「比过了、没差异」**

四闸里的「原型还原」需要视觉比对结果。比对没接时，旧代码传一个默认的
`unhandled = 0`，于是这条闸算出来是绿的，页面上写着「无未处理差异」——
一个从来没跑过的检查，显示成通过。

现在 `CheckCommit` 的第三个参数是 `*int`：传 `nil` 表示「没比对过」，
这条闸会标成 `unknown`、画灰问号、文案写「视觉比对未接入：这条判不了」。
判不了不挡发布（它不表示没过），但页面上不能拿它当证据。

---

## 三、生成器的边界（#97）

「制品生成 ✅」这一栏只说明**机制**是真跑：真落文件、真编译、真跑测试。
它不说明生成出来的东西是「按这个单元的需求定制的实现」。当前生成器的边界：

| 维度 | 现在 | 缺口在哪 |
|---|---|---|
| 语言 | 只有 Go（编译进二进制、digest = 二进制字节 sha256） | #91：按单元/规格选语言与框架前，前端、脚本、多语言服务都被生成成 Go 服务 |
| 模板 | 一条骨架：单文件 `main.go` + 进程内 `map` 存储；资源名与字段按标题选（#93） | #92：没有分层、领域模型、持久化；#94：修复/变更也是整份重渲染，不改已有实现 |
| 规格 | 没有 spec 环节：标题进、骨架出 | #90：specify → clarify → plan → tasks → implement 全缺，AC 是骨架自带的，不是从需求推出来的 |
| 验收连接 | 清单绑提交、没清单不许打 tag（#70） | 清单里的 AC 来自骨架，不是 spec 的 User Story / scenario，所以「测试全过」≠「需求被验过」 |
| 生成质量 | 没有闸 | #95：四闸只看闭合 / 草稿 / digest / 视觉，退化不到「不按规格实现」这一层 |

一句话：**机制真、内容假（截至 #90 之前）。** 拿它当「跑通交付链路」的骨架是成立的；
拿它当「按我的需求生成了代码」是不成立的——那要等 #90/#91/#92/#94，且 #95 得有一条闸把它盯住。
#93 之后至少有一件事变了：生成物里不再夹带模板作者自己拍板的业务口径（费率、上限、种子数据）。

---

## 四、怎么自己验一遍

```bash
# 1. 起环境
docker compose up -d
(cd web && npm ci && npm run build)

# 2. 起服务（仓库根，因为静态根是相对路径 web/dist）
go build -o /tmp/i3-composer-bin ./cmd/i3-composer
/tmp/i3-composer-bin

# 3. 六套验收（v32 第一棒、full 最后；见下面的套件表）
cd web/e2e
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_v32.cjs   # 57 项：四阶段 / 打 tag / 批次发布 / 整批回滚
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_exec.cjs  # 28 项：真测试 / 真构建 / 批次部署 / 真收发
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_flow.cjs  # 37 项：建单元 → 发起 → 越权 → 打 tag → 作废
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_v26.cjs   # 68 项：编排工作台 / 质量报告 / 验收台
BASE=http://127.0.0.1:8080 RUN_TAG=fin node acceptance_full.cjs  # 20 项：从零建单元 → 真跑五步 → 打 tag → 批次
BASE=http://127.0.0.1:8080 node acceptance_plan.cjs              # 12 项：方案页三页签的布局宽度
```

### 套件表（唯一口径）

| 脚本 | 覆盖 | 断言 | 依赖顺序 | 进 CI |
|---|---|---|---|---|
| `acceptance_v32.cjs` | 四阶段、打 tag 进池、项目级批次发布与整批回滚、验收清单绑提交 | 57 | **必须第一棒**：第 2 段断言项目下只有 4 个种子单元 | 否 |
| `acceptance_exec.cjs` | 执行侧证据：真测试条数、内容摘要 digest、真进程与 `/healthz`、pub/sub 真收发、验收清单 | 28 | v32 之后；自带 `RUN_TAG` 建单元 | 否 |
| `acceptance_flow.cjs` | 写路径：建单元 → 建任务 → 发起 → 越权被拒 → 验收 → 打 tag → 作废与重建 | 37 | v32 之后；自带 `RUN_TAG` | 否 |
| `acceptance_v26.cjs` | v2.6 界面与 Agent 编排：digest 闸、试跑与装配、质量报告、验收台、对话拦截 | 68 | v32 之后 | 否 |
| `acceptance_full.cjs` | 从空账本跑完整链路：建单元 → 建任务 → 五步真跑 → 打 tag → 批次 | 20 | **放最后**：会新建单元并真起进程 | 否 |
| `acceptance_plan.cjs` | 方案页（PRD / AD / AT）预览与编辑区的布局宽度（2 个视口） | 12 | 无：只读，不写账本 | 否 |
| `shots_v26.cjs` | 不出断言，只截图 | — | 任意 | 否 |

前五套是「五套验收」（合计 210 项）；第六套 `acceptance_plan.cjs` 只测布局，不依赖 Dapr、不写账本，
以前没人跑也没有文档——现在与其它套件同口径（`SUMMARY n/n passed`），并在这张表里列明。
断言数字以本表为准：改断言时，这里、`README.md` 的验收段、脚本开头注释三处一起改。

**为什么要先编译再跑、而不是 `go run`：** `go run` 把 SIGTERM 送给 `go`
命令本身，编译出来的子进程未必收到。你以为重启了，8080 上还是上一次那个
进程在应答——改的代码没生效，验收却全绿。这个坑是真踩过的。
起之前先 `lsof -nP -iTCP:8080 -sTCP:LISTEN` 看是谁在应答；
跑编译好的二进制，信号直达进程，退出时 `StopAll()` 也才收得掉部署进程。

`acceptance_exec.cjs` 是最能说明问题的一套：它跑完一条交付后，
逐条核对账本里的证据——代码制品能不能编译、测试条数对不对、
digest 是不是内容摘要、部署地址是不是真的活着；还要**在页面上**核对一遍，
那些证据得是人看得见的，不是只躺在 Redis 里。

`acceptance_full.cjs` 从空账本开始（它自己建交付单元），所以**放最后跑**：
它会在账上留下一个新单元，先跑它会让 `acceptance_v32.cjs` 里
「项目下只有 4 个种子单元」那条断言看到多余单元。

Go 侧还有一条进程内的：

```bash
go test ./internal/wf/ -run TestDeliveryChainReallyBuildsTestsAndDeploys -v
```

它不依赖 Dapr，直接把六个 activity 串起来跑：真编译、真测试、真起进程、
真等健康检查。

---

## 五、还没接的那几件事，各自要怎么接

| 事项 | 缺口 | 接口在哪 | 接上要做什么 |
|---|---|---|---|
| 交付构建走 CI 服务 | 构建是本地 `go build`，不是 CI | `internal/pipeline.Build` | 换成调 CI 的实现；上层契约（返回 digest）不变 |
| 真模型 | Agent 判定是本地规则引擎 | `internal/agentexec.decide` | 换成调模型网关；编排图与节点契约不变 |
| 真业务数据 | 工具读仓内语料 | `internal/agentexec.runTool` | 换成调真实接口；工具名与入参映射不变 |
| 真还原对比 | 视觉比对未接：这条闸判不了 | `domain.CheckCommit(..., unhandled *int)` | 传 nil 表示「没比对过」，闸显示「判不了」而不是「无差异」；接上比对后传入差异数 |
| 按规格生成 | 生成内容是骨架，不是本单元的 spec 实现 | `codegen.Spec` / `pipeline.specOf` | #90：补 specify → clarify → plan → tasks → implement 五段，`Spec` 收 spec / plan 而不是只有标题 |
| 生成质量闸 | 四闸不覆盖「是不是按规格/结构可接手」 | `domain.CheckCommit` + 闸表 | #95：加一条结构约定检查（分层存在、领域不依赖 HTTP、存储走接口），退化在账本里看得见 |

这四处都是**换实现，不改结构**：编排、账本、闸门、页面都已经是真的了。

仓库自己的验证已经接到 CI（`.github/workflows/ci.yml`，每个 PR 都跑）。
上面表里的六套 Playwright 验收都不在 CI 里（表里「进 CI」一列全是「否」）：它们要 Dapr sidecar
与真实进程部署，在 CI 上只能跑成假绿，所以在本地跑。

**顺序是硬约束，但不是自动强制的**：`acceptance_v32.cjs` 的前置是干净账本（4 个种子单元），
在跑过别的套件之后跑它，会看到多余单元并红 3 条——那是前提没满足，不是产品缺陷。
想复现「顺序反了会怎样」，在一本跑过 `full` 的账上直接跑 v32 即可。

---

## 六、隔离与并行

同一套 Dapr 上可以同时跑几套账本：

```bash
LEDGER_PREFIX=demo2/ WORKFLOW_NAME=i3-delivery-2 ADDR=:8082 go run ./cmd/i3-composer
```

- `LEDGER_PREFIX`：账本前缀。事实写到 `log:<pbc>:` 前缀不同的键上，互不干扰。
- `WORKFLOW_NAME`：workflow 名。名字不同 = Dapr 里不同的 actor 类型，互不抢工作项。

工作项自己带着账本前缀走（`wf.Input.Ledger`）：它落到哪个进程执行是 Dapr 决定的，
账本归谁由工作项说了算，不由执行它的进程猜。

**一个端口上的硬约束：收事件的那个实例必须在 8080。**

sidecar 是把订阅到的事件用 HTTP 投给应用的，投递地址是
`--app-channel-address` + `--app-port`（`docker-compose.yml` 里是
`host.docker.internal:8080`）。所以：

- 监听 8080 的实例 → 收得到 `tag.created` / `item.changed`，落 `event.received` 事实；
- 监听别的端口的实例 → 能发（发是主动调 sidecar 的 HTTP API），
  但**收不到**，它自己那条 `events` 账本会是空的。

workflow 不受这个限制：work-item 流是应用**主动**连 sidecar 的 gRPC 拉取的，
端口是多少都无所谓。

想多套账本真并行，就给每套各起一个 sidecar（各自的 app-port），
或者让每套用各自独立的 pubsub 组件名。
