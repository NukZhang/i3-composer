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
| 部署（发布批次） | ✅ 真 | 打 tag 进池后，由项目级批次发布启动构建产物，等 `GET /healthz` 返回 200 才记 `deploy.succeeded`。整批回滚只停止该批次启动且仍在运行的进程。 |

发布编排把动态端口标为「进程内部」。从宿主机打开工作台后，可点「核对 PBC 运行」查看该批次的单元、提交号、tag 和 digest。进程被替换、退出或回滚后，该批次入口不可用。缺少实例标识的历史批次仍可查看发布和回滚记录。

DA 发起交付后，工作流完成生成、收敛、提交、测试和构建，写入 `item.verified` 后结束。工作项的「前往验证」打开对应单元，BA 判定验收，SA 从验证页打 tag 后发起项目级发布批次。旧的工作项 `/answer` 路由已退役，历史等待和审批事实仍可从工作项账本读取。

### 交付单元作废与复登

PA 从 `GET /api/units` 读取单元的 `registrySeq`，再调用 `DELETE /api/units/{id}?createdSeq=<registrySeq>` 作废这次登记。列表会移除该单元，详情返回 404。请求指向已更换的登记时返回 409，刷新后可核对当前登记。

同一 ID 可通过 `POST /api/units` 重新登记，并继续使用原事实链。已有业务事实时，应沿用原项目、构成和业务实现。作废期间暂停新建工作项、发起交付和验收判定，原工作项编号仍被保留。

### PBC 人工方案与验收

在方案页保存的人工 PRD、AD、AT 随工作流提交。生成保持人工正文，收敛只更新过期引用。
在 AT 中逐条写 `- AT-01 标题。Given 前提，When 操作，Then 预期。`，也可用 `AC-01` 编号并分行填写。
提交会固化一份待判定清单。BA 在验证页按当前提交逐条判定。清单为空、缺项、编号重复、属于旧提交，或任一条待判定或不通过时，SA 无法打 tag。
新提交重新验收，已打 tag 的历史判定仍可回放。

有活动草稿时，质量报告的交付闸显示制品和版本号，SA 无法为旧 HEAD 新打 tag。重新发起交付会将人工草稿内容提交并实测，通过当前提交的人工验收后才能打 tag。质量报告按提交标明测试和构建证据的归属。已经固化的 tag、历史回放与按 tag 运行的发布批次仍使用原提交和摘要。

### 材料入库业务制品

新建微服务 PBC 时选择「材料入库」业务实现。业务表单、查询 API 和五条机器用例随提交一起构建。BA 在原型、构建预览或验证页启动当前提交的测试实例，可以按人工 AT 确认、拒绝和追溯入库单。SA 打 tag 发布 SIT 后，可以从批次打开对应提交的业务页面。

服务以千分位整数校验余量，将入库、归属快照与扣减后的余量写成一条确认事实。测试和 SIT 在制品卷中分开持久化。入口覆盖脱敏样例，材料字典、来源和计划系统的实时集成留待后续。 [验收证据](selftest-v32/材料入库验收.md)
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
`/api/bills` 返回的是这个进程内存里的真实数据。

**4. Agent 试跑曾经是回放**

以前把定义里存好的样例原样端出来，事实里明写 `source:"sample"`。
那证明不了任何东西：输入变了输出也不会变。
现在按编排图真跑一遍，结论随输入变；样例只作为「没接执行器时的兜底」，
且在界面上标出来。

**5. pub/sub 曾经只发不收，而且 workflow 的推进绕过了它**

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

**6. 种子曾经冒充历史**

种子写的版本号、评测分、发布记录看起来像真干过。现在它们的 actor 是 `seed`，
界面上标「演示数据」；真跑出来的事实 actor 是具体角色，一眼能分开。

**7. 部署成功过，进程却赖着不走**

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

**8. 「没比对过」曾经被显示成「比过了、没差异」**

四闸里的「原型还原」需要视觉比对结果。比对没接时，旧代码传一个默认的
`unhandled = 0`，于是这条闸算出来是绿的，页面上写着「无未处理差异」——
一个从来没跑过的检查，显示成通过。

现在 `CheckCommit` 的第三个参数是 `*int`：传 `nil` 表示「没比对过」，
这条闸会标成 `unknown`、画灰问号、文案写「视觉比对未接入：这条判不了」。
判不了不挡发布（它不表示没过），但页面上不能拿它当证据。

---

## 三、怎么自己验一遍

复测前在构建应用、编译前端和运行脚本的同一工作树执行：

```bash
(
  set -e
  git rev-parse HEAD
  worktree_status=$(git status --porcelain --untracked-files=all)
  if [ -n "$worktree_status" ]; then
    printf '%s\n' '工作树含未提交改动，结果不代表任何提交。' "$worktree_status" >&2
    exit 1
  fi
  printf '%s\n' 'git status --porcelain --untracked-files=all: 空'
)
```

检查通过再构建与运行。归档时保留完整 SHA、工作树状态、账本和运行日志。若特意在开发工作树试验，也必须记录改动并标注结果不代表任何提交。

按仓库约定新建独立 Compose 项目，使用新 Redis 与制品卷，确认 sidecar、应用端口和 `BASE` 都属于本轮环境；不要让验收脚本写入现有开发或生产账本。
有工作区 `../docker-local/compose.yml` 时沿用它和当前启用的附加配置，用唯一的 `docker compose -p` 项目名及空闲宿主机端口隔离。换 `RUN_TAG` 不会清空账本。
[隔离容器复跑命令](selftest-v32/隔离容器复跑.md)给出实际使用的 Compose 覆盖和 Chromium 准备步骤。它从同一个归档构建应用并运行脚本，不使用工作区 Compose 默认指向的主 checkout。

```bash
# 1. 构建前端（容器方式可在构建镜像时完成）
(cd web && npm ci && npm run build)

# 2. 若从宿主机运行应用，先导出本轮设置，再启动
: "${ADDR:?请设置本轮应用的监听地址}"
: "${DAPR_HTTP_ENDPOINT:?请设置本轮 sidecar 的 HTTP 地址}"
: "${DAPR_GRPC_ENDPOINT:?请设置本轮 sidecar 的 gRPC 地址}"
: "${LEDGER_PREFIX:?请设置本轮账本前缀}"
: "${ARTIFACT_DIR:?请设置本轮独立制品目录}"
go build -o /tmp/i3-composer-bin ./cmd/i3-composer
WEB_DIR="$PWD/web/dist" REQUIRE_PUBSUB=1 SEED_DEMO=1 /tmp/i3-composer-bin

# 3. 新终端：显式导出本轮 BASE 后，按顺序各跑一次
(
  set -e
  : "${BASE:?请先设置本轮独立验收实例的 BASE}"
  SHOTS=$(mktemp -d "${TMPDIR:-/tmp}/i3-acceptance.XXXXXX")
  printf '本轮截图与汇总目录：%s\n' "$SHOTS"
  cd web/e2e
  export BASE RUN_TAG="${RUN_TAG:-$(date +%s)}" SHOTS
  node acceptance_v32.cjs   # v3.2：四阶段 / 打 tag / 批次发布 / 整批回滚
  node acceptance_exec.cjs  # 执行侧：真测试 / 真构建 / 批次部署 / 真收发
  node acceptance_flow.cjs  # 写路径：建单元 → 发起 → 越权 → 打 tag
  node acceptance_full.cjs  # 从零：建单元 → 建任务 → 真跑五步 → 打 tag → 批次
  node acceptance_v26.cjs   # v2.6 界面：编排工作台 / 质量报告 / 验收台
)
```

**为什么要先编译再跑、而不是 `go run`：** `go run` 把 SIGTERM 送给 `go`
命令本身，编译出来的子进程未必收到。你以为重启了，8080 上还是上一次那个
进程在应答——改的代码没生效，验收却全绿。这个坑是真踩过的。
起之前先 `lsof -nP -iTCP:8080 -sTCP:LISTEN` 看是谁在应答；
跑编译好的二进制，信号直达进程，退出时 `StopAll()` 也才收得掉部署进程。

`acceptance_exec.cjs` 是最能说明问题的一套：它跑完一条交付后，
逐条核对账本里的证据——代码制品能不能编译、测试条数对不对、
digest 是不是内容摘要、部署地址是不是真的活着；还要**在页面上**核对一遍，
那些证据得是人看得见的，不是只躺在 Redis 里。

`acceptance_v32.cjs` 先检查项目 `p1` 的四个演示种子单元和发布历史，发现额外或缺失的单元、已有发布批次时会在浏览器启动前退出。
五套脚本均先核对 `BASE` 的 `/api/health`，只有 `pubsub.checked` 和 `pubsub.ok` 同时为真才运行。容器中应保留 `web/e2e/` 的脚本布局和种子文件 `internal/seed/agents.json`。
它和 `acceptance_full.cjs` 都会创建新单元，后者在本轮账本中新建自己的交付单元来验从零交付。
下一轮须切换到新的账本，而不只是换 `RUN_TAG`。

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
| 交付构建走 CI 服务 | 构建是本地 `go build`，不是 CI | `internal/pipeline.Build` | 换成调 CI 的实现；上层契约（返回 digest）不变 |
| 真模型 | Agent 判定是本地规则引擎 | `internal/agentexec.decide` | 换成调模型网关；编排图与节点契约不变 |
| 真业务数据 | 工具读仓内语料 | `internal/agentexec.runTool` | 换成调真实接口；工具名与入参映射不变 |
| 真还原对比 | 视觉比对未接：这条闸判不了 | `domain.CheckCommit(..., unhandled *int)` | 传 nil 表示「没比对过」，闸显示「判不了」而不是「无差异」；接上比对后传入差异数 |

这四处都是**换实现，不改结构**：编排、账本、闸门、页面都已经是真的了。

仓库自己的验证已经接到 CI（`.github/workflows/ci.yml`，每个 PR 都跑）。
五套 Playwright 验收不在 CI 里：它们要 Dapr sidecar 与真实进程部署，
在 CI 上只能跑成假绿，所以在本地跑。

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
