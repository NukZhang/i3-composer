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
| 制品生成 | ✅ 真 | `internal/codegen` 产出 markdown 正文 + 可编译的 Go 模块（`go.mod` / `main.go` / `main_test.go`） |
| 测试 | ✅ 真 | `go test -count=1 -v ./...` 的真实输出；不过就阻断链路（`test.run` 事实带 `failures`） |
| 构建 | ✅ 真 | `go build -trimpath` 出二进制；digest = **二进制字节的 sha256**（内容寻址），不是树摘要拼的 |
| 部署 | ✅ 真 | 起构建产物为进程，等 `GET /healthz` 真的返回 200 才记 `deploy.succeeded`；URL 与 pid 都进事实 |
| Agent 试跑 / 对话验收 | ✅ 真（本地判定） | `internal/agentexec` 按编排图逐节点执行：读会话、检索知识库、跑合规规则、判分支、转人工；**输入变了结论就变** |
| pub/sub | ✅ 真（收发闭环） | 声明式订阅 `components/subscription.yaml` → sidecar 建 consumer group → 事件回到 `/api/events/{topic}` → 落 `event.received` 事实 |
| 种子数据 | ⚠️ 演示数据（已标注） | actor 写作 `seed`，页面上标「演示数据」；真跑出来的事实 actor 是 `x-fe`/`x-qa`/`x-ops`/`SA` |
| CI（Jenkins / GitHub Actions） | ❌ 未接 | 本地构建是**真的编译**，但它不是 CI；接真 CI 只需换掉 `internal/pipeline` 的 Build 实现 |
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
`/api/bills` 返回的是这个进程内存里的真实数据。

**4. Agent 试跑曾经是回放**

以前把定义里存好的样例原样端出来，事实里明写 `source:"sample"`。
那证明不了任何东西：输入变了输出也不会变。
现在按编排图真跑一遍，结论随输入变；样例只作为「没接执行器时的兜底」，
且在界面上标出来。

**5. pub/sub 曾经只发不收**

事件发进 Redis stream 就没人消费了——看起来像消息总线在工作，其实是单向的。
现在有声明式订阅，sidecar 建 consumer group，事件回到 BFF 落成
`event.received` 事实，页面上能看到「收到的事件」。

**6. 种子曾经冒充历史**

种子写的版本号、评测分、发布记录看起来像真干过。现在它们的 actor 是 `seed`，
界面上标「演示数据」；真跑出来的事实 actor 是具体角色，一眼能分开。

---

## 三、怎么自己验一遍

```bash
# 1. 起环境
docker compose up -d
(cd web && npm ci && npm run build)

# 2. 起服务（仓库根，因为静态根是相对路径 web/dist）
go run ./cmd/i3-composer

# 3. 四套验收
cd web/e2e
BASE=http://127.0.0.1:8080 node acceptance_exec.cjs   # 执行侧：真测试/真构建/真部署/真收发
BASE=http://127.0.0.1:8080 node acceptance_flow.cjs  # 写路径：建任务 → 发起 → 拍板 → 发布
BASE=http://127.0.0.1:8080 node acceptance_v26.cjs   # v2.6 界面：编排工作台 / 质量报告 / 验收台
BASE=http://127.0.0.1:8080 node acceptance_full.cjs  # 从零：建单元 → 建任务 → 真跑六步 → 发布
```

`acceptance_exec.cjs` 是最能说明问题的一套：它跑完一条交付后，
逐条核对账本里的证据——代码制品能不能编译、测试条数对不对、
digest 是不是内容摘要、部署地址是不是真的活着；还要**在页面上**核对一遍，
那些证据得是人看得见的，不是只躺在 Redis 里。

`acceptance_full.cjs` 从空账本开始（它自己建交付单元），所以**放最后跑**：
它会在账上留下一个新单元，先跑它会让 `acceptance_v26.cjs` 里
「种子单元只剩三个」那条断言看到四个单元。

Go 侧还有一条进程内的：

```bash
go test ./internal/wf/ -run TestDeliveryChainReallyBuildsTestsAndDeploys -v
```

它不依赖 Dapr，直接把六个 activity 串起来跑：真编译、真测试、真起进程、
真等健康检查。

---

## 四、还没接的那几件事，各自要怎么接

| 事项 | 缺口 | 接口在哪 | 接上要做什么 |
|---|---|---|---|
| 真 CI | 构建是本地 `go build`，不是 CI | `internal/pipeline.Build` | 换成调 CI 的实现；上层契约（返回 digest）不变 |
| 真模型 | Agent 判定是本地规则引擎 | `internal/agentexec.decide` | 换成调模型网关；编排图与节点契约不变 |
| 真业务数据 | 工具读仓内语料 | `internal/agentexec.runTool` | 换成调真实接口；工具名与入参映射不变 |
| 真还原对比 | 视觉比对未接，四闸第三条恒过 | `domain.CheckCommit(unhandled)` | 接上比对结果传入 |

这四处都是**换实现，不改结构**：编排、账本、闸门、页面都已经是真的了。

---

## 五、隔离与并行

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
