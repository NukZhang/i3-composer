// Package seed 提供可演示的起点数据。
//
// 数据本身来自原型 v2.6（AGENT_META / WORKERS / AG_FLOW / KB_META / VCHECK），
// 这里只做一次性的「把原型演示数据转成事实」的工作：种子必须是事实，
// 不能是前端内存——否则刷新就没了，那正是 v2.1 之前的老问题。
//
// 但种子也必须诚实：
//
//   - 它写的每条事实，actor 都是 seed（不是「张昆」「PA」），
//     页面上一眼能看出「这是演示数据，不是真发生的」。
//   - 制品带真内容：文档有正文，代码是可编译的 Go 模块。
//   - 提交的 digest 是真构建出来的（跑一次 go build 拿二进制摘要），
//     不是拿树摘要拼一个假的 sha256:baseline。
//
// 想从零开始验证全链路：SEED_DEMO=0 —— 账本就是空的，
// 从建单元、建任务到发起、测试、构建、部署、发布全程自己走。
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/zhangkun/i3-composer/internal/codegen"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

//go:embed agents.json
var agentsJSON []byte

//go:embed checks.json
var checksJSON []byte

// ActorSeed 是种子事实的作者标记。
//
// 页面据此把「演示数据」和「真发生的事」区分开：种子里的一切都是
// 造出来给人看的起点，不是工作记录。
const ActorSeed = "seed"

// BuildFunc 真的构建一次，返回产物 digest 与「是否产出了可执行文件」。
//
// 种子用它给基线提交一个真 digest：种子说「这棵树构建过」，
// 那 digest 就得是真的构建出来的，而不是树摘要拼的。
type BuildFunc func(ctx context.Context, pbc, commit string, files map[string]string) (digest string, binary bool, err error)

// Options 是种子的可选项。
type Options struct {
	// Build 为 nil 时不构建，基线提交就没有 digest（四闸会如实变红）。
	Build BuildFunc
}

// Agents 解出 7 名数字员工的定义（v2.6：新增 X-质检员，X-测试工程师回测试执行）。
// 返回的是值拷贝，调用方可以随便改。
func Agents() ([]domain.Agent, error) {
	var out []domain.Agent
	if err := json.Unmarshal(agentsJSON, &out); err != nil {
		return nil, fmt.Errorf("解析 seed/agents.json 失败：%w", err)
	}
	for i := range out {
		// 种子代表「已经构建过、评测过」的状态：先算 digest，再把最近一次
		// 评测标在同一 digest 上，否则「评测针对旧版本」的闸会误报。
		d := domain.AgentDigest(out[i])
		out[i].Digest = d
		if len(out[i].Runs) > 0 {
			out[i].Runs[0].Digest = d
		}
	}
	return out, nil
}

// checkSpec 是一个单元的验收清单与历史 bug。
type checkSpec struct {
	Checks  []domain.Check  `json:"checks"`
	Defects []domain.Defect `json:"defects"`
}

// Checks 解出三个单元的真人验收清单（原型 VCHECK / vBugs）。
func Checks() (map[string]checkSpec, error) {
	var out map[string]checkSpec
	if err := json.Unmarshal(checksJSON, &out); err != nil {
		return nil, fmt.Errorf("解析 seed/checks.json 失败：%w", err)
	}
	return out, nil
}

// Unit 是种子里的交付单元登记。进度（提交/tag）由各自的 PBC 日志产生，
// 这里只登记「项目里有哪些单元、各自是什么构成」。
type Unit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Kind    string `json:"kind"`
	AgentID string `json:"agentId,omitempty"`
}

// Units 对应原型 v2.6 的三个单元：fee（纯 PBC）、qa（Agent）、dun（hybrid）。
// v2.4 删掉了重复的客服工单 PBC：三个单元各代表一种构成。
func Units() []Unit {
	return []Unit{
		{ID: "fee", Name: "收费缴费", Code: "PBC-FEE", Kind: "pbc"},
		{ID: "qa", Name: "交付质检 Agent", Code: "AGENT-QA", Kind: "agent", AgentID: "x-qc"},
		{ID: "dun", Name: "智能催缴", Code: "PBC-DUN + AGENT-DUN", Kind: "hybrid", AgentID: "x-dun"},
	}
}

// removedUnits 是从演示项目里撤掉的单元（工单与收费重复）。
var removedUnits = []string{"ticket"}

// Run 写演示起点。可重复执行：每条日志各自判空，已存在的日志原样保留。
//
// 为什么按日志判空而不是「整体判空」：v2.1 时期只种了 fee，v2.6 又迁了
// 单元与 Agent 定义。整体判空会让老环境的账本永远补不上新结构，
// 而按日志 + 按 OpID 判空既能补新，又不会给已有数据重复种。
func Run(ctx context.Context, log *store.Store, opts Options) error {
	// 先种三个单元自己的日志（制品、基线、提交、工作项），
	// 再种项目级的登记与 BRD —— 顺序反了会让单元日志提前非空、跳过种子。
	if err := seedUnits(ctx, log, opts); err != nil {
		return err
	}
	// 老账本（v2.1/v2.3）里的制品是空壳：没有正文、没有代码。
	// 补一份真内容上去，否则「构建/测试/部署」这三步对着空壳跑，
	// 等于什么都没验证。
	if err := backfillContent(ctx, log, opts); err != nil {
		return err
	}
	if err := seedProject(ctx, log); err != nil {
		return err
	}
	// v2.7：工作空间 → 项目。要先有项目，单元才有地方挂。
	if err := seedProjects(ctx, log); err != nil {
		return err
	}
	if err := seedAgents(ctx, log); err != nil {
		return err
	}
	return seedChecks(ctx, log)
}

// backfillContent 给老账本补真内容。
//
// v2.1/v2.3 时期种子只写了版本号和一句话，内容是空的。现在的执行侧
// 要真的编译、真的跑测试，所以那些制品必须带上真代码与真文档。
//
// 补的方式是一次正常的「变更 + 提交」：新版本带上内容，引用闭合，
// 再真构建一次拿 digest。它是一条事实，不是内存里的补丁。
func backfillContent(ctx context.Context, log *store.Store, opts Options) error {
	for _, u := range Units() {
		facts, err := log.Load(ctx, u.ID)
		if err != nil {
			return err
		}
		if len(facts) == 0 {
			continue // 空日志由 seedUnits 正常种
		}
		v, err := store.Project(u.ID, facts)
		if err != nil {
			return err
		}
		if len(v.Commits) == 0 {
			continue
		}
		head := v.Commits[0]
		codeVer, ok := findVersion(v.Versions, domain.Code, head.Tree[domain.Code])
		if !ok || len(codeVer.Files) > 0 {
			continue // 已经有真内容
		}
		spec := pbcSpec{title: "补齐制品内容：让构建与测试有真实输入", kind: u.Kind}
		if err := writeContentVersion(ctx, log, u, spec, opts); err != nil {
			return err
		}
	}
	return nil
}

// findVersion 在版本线里按版本号找一版。
func findVersion(versions map[domain.Artifact][]domain.Version, t domain.Artifact, v string) (domain.Version, bool) {
	for _, x := range versions[t] {
		if x.V == v {
			return x, true
		}
	}
	return domain.Version{}, false
}

// writeContentVersion 为一个单元写一整套带内容的制品版本 + 一次提交。
func writeContentVersion(ctx context.Context, log *store.Store, u Unit, spec pbcSpec, opts Options) error {
	const at = "11-24 12:00"
	cspec := codegen.Spec{
		UnitID: u.ID, UnitName: u.Name, Kind: u.Kind,
		Title: spec.title, Change: "feature", At: at,
	}
	docs := codegen.Docs(cspec)
	code := map[string]string{}
	if u.Kind == "agent" {
		code = map[string]string{
			"agent/definition.json": codegen.JSON(map[string]any{
				"unit": u.ID, "name": u.Name, "version": "1.0.1",
				"title": spec.title, "generated": at,
				"stages": []string{"方案", "原型", "构建", "验证", "发布"},
			}),
			"agent/README.md": "# " + u.Name + " · Agent 定义\n\n> 演示种子 · " + at + "\n\n" +
				"本单元交付的是一份编排定义，不是一个进程。\n",
		}
	} else {
		for _, f := range codegen.Files(cspec) {
			code[f.Path] = f.Content
		}
	}

	// 新版本号从当前最新版本往后推。
	facts, err := log.Load(ctx, u.ID)
	if err != nil {
		return err
	}
	v, err := store.Project(u.ID, facts)
	if err != nil {
		return err
	}
	newV := map[domain.Artifact]string{}
	for _, t := range domain.Order {
		nv, err := domain.NextVersion(domain.Latest(v.Versions, t))
		if err != nil {
			return err
		}
		newV[t] = nv
	}
	// 衍生引用按「新版本表」算，保证提交时引用闭合。
	latestOf := func(t domain.Artifact) string { return newV[t] }

	vers := []domain.Version{
		{T: domain.PRD, V: newV[domain.PRD], At: at, By: ActorSeed, Source: "生成", St: domain.StAccepted,
			MD: docs[domain.PRD], Note: spec.title},
		{T: domain.Proto, V: newV[domain.Proto], At: at, By: "x-ui", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.PRD], DF: domain.DeriveRefs(domain.Proto, latestOf), Note: "三屏：列表 / 生成 / 对账"},
		{T: domain.AD, V: newV[domain.AD], At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.AD], DF: domain.DeriveRefs(domain.AD, latestOf), Note: "单库设计"},
		{T: domain.AT, V: newV[domain.AT], At: at, By: "x-qa", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.AT], DF: domain.DeriveRefs(domain.AT, latestOf), Note: "5 条 Given/When/Then"},
		{T: domain.Backlog, V: newV[domain.Backlog], At: at, By: "x-ba", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.Backlog], DF: domain.DeriveRefs(domain.Backlog, latestOf), Note: "story / task 拆分"},
		{T: domain.Code, V: newV[domain.Code], At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			Files: code, DF: domain.DeriveRefs(domain.Code, latestOf), Note: "基线代码"},
	}
	for _, ver := range vers {
		op := "seed/v26/content/" + u.ID + "/" + ver.T.String()
		if _, err := log.AppendOp(ctx, u.ID, op, store.FactArtifactDrafted, ver.By, ver); err != nil {
			return err
		}
	}
	tree := domain.Tree{}
	for _, t := range domain.Order {
		tree[t] = newV[t]
	}
	digest := ""
	binary := false
	if opts.Build != nil {
		d, b, err := opts.Build(ctx, u.ID, newV[domain.Code], code)
		if err != nil {
			return fmt.Errorf("补内容后构建失败（%s）：%w", u.ID, err)
		}
		digest, binary = d, b
	}
	no := "1.0.2"
	if len(v.Commits) > 0 {
		if n, err := domain.NextVersion(v.Commits[0].No); err == nil {
			no = n
		}
	}
	c := domain.Commit{
		No: no, Tree: tree, By: ActorSeed, At: at, Msg: spec.title, Type: "feature",
		TreeSHA: domain.TreeSHA(tree), Digest: digest,
	}
	if _, err := log.AppendOp(ctx, u.ID, "seed/v26/content/"+u.ID+"/commit", store.FactCommitCreated, ActorSeed, c); err != nil {
		return err
	}
	if digest != "" {
		if _, err := log.AppendOp(ctx, u.ID, "seed/v26/content/"+u.ID+"/build", store.FactBuildSucceeded, ActorSeed,
			map[string]any{"commit": no, "digest": digest, "binary": binary}); err != nil {
			return err
		}
	}
	return nil
}

// seedUnits 写三个交付单元各自的日志。
func seedUnits(ctx context.Context, log *store.Store, opts Options) error {
	// fee：完整的制品基线 + 三条工作项。
	if err := seedPBC(ctx, log, "fee", "收费缴费", pbcSpec{
		title: "收费缴费基线",
		items: []store.Item{
			{ID: "US-01", Title: "业主账单自助查询与支付", PT: 5, St: "done", Owner: "DA",
				Kind: "Story", PBC: "fee", Type: "new", Mode: "guided", Env: "test",
				Run: "published", Version: "1.0.2", Tag: "v1.0.0", By: ActorSeed},
			{ID: "US-02", Title: "管家批量生成缴费单", PT: 3, St: "doing", Owner: "DA",
				Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
				Run: "waiting", Step: 6, By: ActorSeed},
			{ID: "US-03", Title: "报修工单 SLA 超时提醒", PT: 3, St: "todo", Owner: "BA",
				Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
				Run: "idea", By: ActorSeed},
		},
	}, opts); err != nil {
		return err
	}

	// qa：Agent 单元。制品树照样齐全——Agent 也要走五阶段。
	if err := seedPBC(ctx, log, "qa", "交付质检 Agent", pbcSpec{
		title: "质检 Agent 基线",
		kind:  "agent",
	}, opts); err != nil {
		return err
	}

	// dun：组合单元。服务侧验收 20/22 —— 故意不是全过：
	// 组合项最怕「服务验完就发」，一个全绿的面板演示不出来这件事。
	return seedPBC(ctx, log, "dun", "智能催缴", pbcSpec{
		title: "催缴服务与催缴 Agent 一起交付",
		kind:  "hybrid",
		svc: &domain.SvcVerify{Pass: 20, Total: 22, Fails: []domain.FailGroup{
			{N: "逾期 1-3 天误催", C: 1, Ex: "逾期 2 天仍进入催告模板"},
			{N: "频次控制边界", C: 1, Ex: "7 天内第 4 次触达未被拦截"},
		}},
	}, opts)
}

// pbcSpec 是一个 PBC 的种子内容。
type pbcSpec struct {
	title string // 基线提交说明
	kind  string // 单元构成（决定代码制品是 Go 模块还是 Agent 定义）
	svc   *domain.SvcVerify
	items []store.Item
}

// empty 判定某条日志是否还没有任何事实。日志不存在也算空。
func empty(ctx context.Context, log *store.Store, pbc string) (bool, error) {
	if err := log.Ensure(ctx, pbc); err != nil {
		return false, err
	}
	facts, err := log.Load(ctx, pbc)
	if err != nil {
		return false, err
	}
	return len(facts) == 0, nil
}

func seedPBC(ctx context.Context, log *store.Store, pbc, name string, spec pbcSpec, opts Options) error {
	ok, err := empty(ctx, log, pbc)
	if err != nil || !ok {
		return err
	}
	if _, err := log.Append(ctx, pbc, store.FactPBCCreated, ActorSeed, map[string]string{"name": name}); err != nil {
		return err
	}
	if err := seedBaseline(ctx, log, pbc, name, spec, opts); err != nil {
		return err
	}
	if spec.svc != nil {
		if _, err := log.Append(ctx, pbc, store.FactSvcVerified, ActorSeed, *spec.svc); err != nil {
			return err
		}
	}
	return seedItems(ctx, log, pbc, spec.items)
}

// seedProject 写交付单元登记与 BRD 版本线（同一条 units 日志）。
//
// 老账本（v2.1/v2.3）里可能还挂着 ticket 单元、qa 单元指向 x-qa：
// 这些用 OpID 幂等的事实就地迁移成 v2.6 的构成。
func seedProject(ctx context.Context, log *store.Store) error {
	if err := log.Ensure(ctx, "units"); err != nil {
		return err
	}
	facts, err := log.Load(ctx, "units")
	if err != nil {
		return err
	}
	uv, err := store.ProjectUnits(facts)
	if err != nil {
		return err
	}
	have := map[string]store.Unit{}
	for _, u := range uv.Units {
		have[u.ID] = u
	}
	for _, u := range Units() {
		cur, ok := have[u.ID]
		if ok && cur.Kind == u.Kind && cur.AgentID == u.AgentID {
			continue
		}
		// OpID 里带上构成：构成变了就再落一条（ProjectUnits 按 id 覆盖）。
		op := "seed/v26/unit/" + u.ID + "/" + u.Kind + "/" + u.AgentID
		if _, err := log.AppendOp(ctx, "units", op, store.FactUnitCreated, ActorSeed, map[string]any{
			"id": u.ID, "name": u.Name, "code": u.Code, "kind": u.Kind, "agentId": u.AgentID,
			"proj": store.DefaultProject,
		}); err != nil {
			return err
		}
	}
	for _, id := range removedUnits {
		if _, ok := have[id]; !ok {
			continue
		}
		if _, err := log.AppendOp(ctx, "units", "seed/v26/unit/removed/"+id, store.FactUnitRemoved, ActorSeed,
			map[string]any{"id": id}); err != nil {
			return err
		}
	}
	brd, err := store.ProjectBRD(facts, store.DefaultProject)
	if err != nil {
		return err
	}
	if len(brd.Vers) == 0 {
		return seedBRD(ctx, log)
	}
	return nil
}

// seedAgents 写 7 名数字员工的定义与「已构建」状态。
//
// v2.6 的差异：编排（Flow）与知识库（KB）进了定义；qa 单元改挂 x-qc；
// X-测试工程师回到测试执行。定义升级用 OpID 幂等，老账本原地补新。
func seedAgents(ctx context.Context, log *store.Store) error {
	if err := log.Ensure(ctx, "agents"); err != nil {
		return err
	}
	facts, err := log.Load(ctx, "agents")
	if err != nil {
		return err
	}
	av, err := store.ProjectAgents(facts)
	if err != nil {
		return err
	}
	as, err := Agents()
	if err != nil {
		return err
	}
	for _, a := range as {
		cur := av.Agents[a.ID]
		// 没有定义、还是 v2.3 的无编排定义 → 升到 v2.6（OpID 幂等）。
		if cur == nil || cur.Flow == nil {
			if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/"+a.ID, store.FactAgentDefined, ActorSeed, a); err != nil {
				return err
			}
		}
		// 种子里的 Agent 已经构建过：种子定义的 digest 就是 BuiltDigest。
		// 这一条决定了「digest 一致」闸在演示数据上是绿的。
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/"+a.ID+"/built", store.FactAgentBuilt, ActorSeed,
			map[string]any{"agentId": a.ID, "ver": a.Ver, "digest": a.Digest}); err != nil {
			return err
		}
	}
	// v2.6 只有 X-运维工程师处于「已发布」（五阶段全 done）。
	// 老账本里 x-qa 曾以「质检」身份发布过：撤下它，避免一页上出现
	// 「流程是测试执行、发布记录是质检」的自相矛盾。
	if old := av.Agents["x-qa"]; old != nil && old.Published {
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-qa/retire", store.FactAgentRetired, ActorSeed,
			map[string]any{"agentId": "x-qa"}); err != nil {
			return err
		}
	}
	if old := av.Agents["x-ops"]; old == nil || !old.Published {
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-ops/publish", store.FactAgentPublish, ActorSeed,
			map[string]any{"agentId": "x-ops", "ver": "1.2.1", "digest": agentDigestOf(as, "x-ops"), "tag": "v1.2.1"}); err != nil {
			return err
		}
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-ops/rollout", store.FactAgentRollout, ActorSeed,
			map[string]any{"agentId": "x-ops", "pct": 100}); err != nil {
			return err
		}
	}
	return nil
}

func agentDigestOf(as []domain.Agent, id string) string {
	for _, a := range as {
		if a.ID == id {
			return a.Digest
		}
	}
	return ""
}

// seedBRD 是原型 BRD_MD 的起点：v1.2 草稿，§4 待补。
func seedBRD(ctx context.Context, log *store.Store) error {
	text := `# 悦府物业 · 收费与工单数字化 BRD

> v1.2 · 渐进编写中 · 负责人 BA

## 1. 业务背景
悦府物业现有 14 个在管项目，收费与报修依赖线下台账。

## 2. 业务目标
- 账单可按账期批量生成，规则可配置
- 业主可自助查看与支付账单
- 报修工单全流程可追溯

## 3. 范围（已确认）
### 3.1 收费业务
物业费、车位费、水电分摊三类费用项；支持按账期与楼栋批量生成。
### 3.2 客服工单
报修受理、派单调度、SLA 与回访。

## 4. 收费范围与账期规则（待补）

## 5. 约束
- 需与金蝶财务系统对账口径一致
- 私有化部署，不依赖外部 SaaS`
	for _, v := range []struct{ V, At, Note, St string }{
		{"v1.0", "11-18 10:20", "背景与目标", "accepted"},
		{"v1.1", "11-19 15:40", "补范围 §3", "accepted"},
		{"v1.2", "11-21 09:10", "待补 §4 账期规则", "draft"},
	} {
		op := "seed/brd/" + v.V
		if _, err := log.AppendOp(ctx, "units", op, store.FactBRDSaved, ActorSeed, map[string]any{
			"proj": store.DefaultProject,
			"v":    v.V, "at": v.At, "by": ActorSeed, "st": v.St, "note": v.Note, "text": text,
		}); err != nil {
			return err
		}
	}
	return nil
}

// seedProjects 种 v2.7 的工作空间与项目。
//
// 演示起点仍是「悦府物业 / 收费与工单数字化」这一组；区别是它现在
// 是账本里的一条事实，而不是写死在前端的常量。已有账本不重复种。
func seedProjects(ctx context.Context, log *store.Store) error {
	if err := log.Ensure(ctx, "projects"); err != nil {
		return err
	}
	facts, err := log.Load(ctx, "projects")
	if err != nil {
		return err
	}
	if len(facts) > 0 {
		return nil
	}
	if _, err := log.AppendOp(ctx, "projects", "seed/v27/ws/w1", store.FactWorkspaceCreated, ActorSeed,
		map[string]any{"id": "w1", "name": "悦府物业", "use": "org"}); err != nil {
		return err
	}
	_, err = log.AppendOp(ctx, "projects", "seed/v27/proj/p1", store.FactProjectCreated, ActorSeed,
		map[string]any{
			"id": "p1", "ws": "w1", "name": "收费与工单数字化", "owner": "张昆",
			"brd": map[string]any{"v": "v1.2", "file": "悦府物业收费与工单需求说明书.docx", "size": "2.4 MB", "at": "11-20 14:20"},
		})
	return err
}

// seedBaseline 写 v1.0.1 的制品线与基线提交。//
// 制品带真内容：文档有 markdown 正文，代码是可编译的 Go 模块；
// 基线提交的 digest 是真构建出来的（二进制字节的 sha256），
// 所以「镜像与树一致」这条闸在演示数据上也是真的绿。
func seedBaseline(ctx context.Context, log *store.Store, pbc, name string, spec pbcSpec, opts Options) error {
	const at = "11-20 17:20"
	cspec := codegen.Spec{
		UnitID:   pbc,
		UnitName: name,
		Kind:     spec.kind,
		Title:    spec.title,
		Change:   "new",
		At:       at,
	}
	docs := codegen.Docs(cspec)
	code := map[string]string{}
	for _, f := range codegen.Files(cspec) {
		code[f.Path] = f.Content
	}
	if cspec.Kind == "agent" {
		// Agent 单元的代码制品是编排定义本身（不是 Go 服务）。
		code = map[string]string{
			"agent/definition.json": codegen.JSON(map[string]any{
				"unit": pbc, "name": name, "version": "1.0.1",
				"title": spec.title, "generated": at,
				"stages": []string{"方案", "原型", "构建", "验证", "发布"},
			}),
			"agent/README.md": "# " + name + " · Agent 定义\n\n> 演示种子 · " + at + "\n\n" +
				"本单元交付的是一份编排定义，不是一个进程。\n",
		}
	}

	vers := []domain.Version{
		{T: domain.PRD, V: "1.0.1", At: at, By: ActorSeed, Source: "生成", St: domain.StAccepted,
			MD: docs[domain.PRD], Note: spec.title},
		{T: domain.Proto, V: "1.0.1", At: at, By: "x-ui", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.PRD], DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}}, Note: "三屏：列表 / 生成 / 对账"},
		{T: domain.AD, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.AD], DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.Proto, V: "1.0.1"}}, Note: "单库设计"},
		{T: domain.AT, V: "1.0.1", At: at, By: "x-qa", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.AT], DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.AD, V: "1.0.1"}}, Note: "5 条 Given/When/Then"},
		{T: domain.Backlog, V: "1.0.1", At: at, By: "x-ba", Source: "生成", St: domain.StAccepted,
			MD: docs[domain.Backlog], DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}}, Note: "story / task 拆分"},
		{T: domain.Code, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			Files: code, DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}, {T: domain.Backlog, V: "1.0.1"}}, Note: "基线代码"},
	}
	for _, v := range vers {
		op := "seed/" + pbc + "/baseline/" + v.T.String()
		if _, err := log.AppendOp(ctx, pbc, op, store.FactArtifactDrafted, v.By, v); err != nil {
			return err
		}
	}

	tree := domain.Tree{}
	for _, t := range domain.Order {
		tree[t] = "1.0.1"
	}

	// 真构建一次：digest 必须是构建产物的摘要。
	digest := ""
	binary := false
	if opts.Build != nil {
		d, b, err := opts.Build(ctx, pbc, "1.0.1", code)
		if err != nil {
			return fmt.Errorf("基线构建失败（%s）：%w", pbc, err)
		}
		digest, binary = d, b
	}

	c := domain.Commit{
		No: "1.0.1", Tree: tree, By: ActorSeed, At: at, Msg: spec.title, Type: "new",
		TreeSHA: domain.TreeSHA(tree), Digest: digest,
	}
	if _, err := log.AppendOp(ctx, pbc, "seed/"+pbc+"/baseline/commit", store.FactCommitCreated, ActorSeed, c); err != nil {
		return err
	}
	if digest != "" {
		// 构建事实：digest 与「有无独立进程」都记下来，
		// 部署那一步才知道该起进程还是该说「没有独立进程」。
		if _, err := log.AppendOp(ctx, pbc, "seed/"+pbc+"/baseline/build", store.FactBuildSucceeded, ActorSeed,
			map[string]any{"commit": "1.0.1", "digest": digest, "binary": binary}); err != nil {
			return err
		}
	}
	// 基线也打一个 tag：演示数据里「发过一次」是常态。
	if _, err := log.AppendOp(ctx, pbc, "seed/"+pbc+"/baseline/tag", store.FactTagCreated, ActorSeed,
		map[string]any{"tag": "v1.0.0", "commit": "1.0.1"}); err != nil {
		return err
	}
	return nil
}

func seedItems(ctx context.Context, log *store.Store, pbc string, items []store.Item) error {
	for _, it := range items {
		if it.Steps == nil {
			it.Steps = store.StepPlan(it.Env)
		}
		for i := range it.Steps {
			switch it.Run {
			case "published":
				it.Steps[i].St = "done"
				it.Steps[i].At = "11-22 15:18"
			case "waiting":
				if i < 6 {
					it.Steps[i].St = "done"
					it.Steps[i].At = "11-22 15:18"
				}
			}
		}
		op := "seed/" + pbc + "/item/" + it.ID
		if _, err := log.AppendOp(ctx, pbc, op, store.FactItemCreated, ActorSeed, it); err != nil {
			return err
		}
	}
	return nil
}

// seedChecks 写真人验收清单与历史 bug（原型 VCHECK / vBugs）。
// 判定归人，种子只给「当前状态」，后续 pass / fail 由 check.decided 事实改写。
//
// 每条都用 OpID 幂等：重复启动不会把清单写两遍。
func seedChecks(ctx context.Context, log *store.Store) error {
	specs, err := Checks()
	if err != nil {
		return err
	}
	for unit, spec := range specs {
		if _, err := log.AppendOp(ctx, unit, "seed/v26/checks/"+unit, store.FactCheckDefined, ActorSeed,
			map[string]any{"items": spec.Checks}); err != nil {
			return err
		}
		for _, d := range spec.Defects {
			if _, err := log.AppendOp(ctx, unit, "seed/v26/defect/"+d.ID, store.FactDefect, ActorSeed,
				d); err != nil {
				return err
			}
		}
	}
	return nil
}
