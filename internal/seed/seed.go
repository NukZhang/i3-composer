// Package seed 提供可演示的起点数据。
//
// 数据本身来自原型 v2.6（AGENT_META / WORKERS / AG_FLOW / KB_META / VCHECK），
// 这里只做一次性的「把原型演示数据转成事实」的工作：种子必须是事实，
// 不能是前端内存——否则刷新就没了，那正是 v2.1 之前的老问题。
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

//go:embed agents.json
var agentsJSON []byte

//go:embed checks.json
var checksJSON []byte

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
func Run(ctx context.Context, log *store.Store) error {
	// 先种三个单元自己的日志（制品、基线、提交、工作项），
	// 再种项目级的登记与 BRD —— 顺序反了会让单元日志提前非空、跳过种子。
	if err := seedUnits(ctx, log); err != nil {
		return err
	}
	if err := seedProject(ctx, log); err != nil {
		return err
	}
	if err := seedAgents(ctx, log); err != nil {
		return err
	}
	return seedChecks(ctx, log)
}

// seedUnits 写三个交付单元各自的日志。
func seedUnits(ctx context.Context, log *store.Store) error {
	// fee：完整的制品基线 + 三条工作项。
	if err := seedPBC(ctx, log, "fee", "收费缴费", pbcSpec{
		prdNote: "收费与工单基线", msg: "基线：PRD 手动上传 + 三件制品生成", digest: "sha256:baseline",
		items: []store.Item{
			{ID: "US-01", Title: "业主账单自助查询与支付", PT: 5, St: "done", Owner: "DA",
				Kind: "Story", PBC: "fee", Type: "new", Mode: "guided", Env: "test",
				Run: "published", Version: "1.0.2", Tag: "v1.0.0", By: "BA"},
			{ID: "US-02", Title: "管家批量生成缴费单", PT: 3, St: "doing", Owner: "DA",
				Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
				Run: "waiting", Step: 6, By: "PA"},
			{ID: "US-03", Title: "报修工单 SLA 超时提醒", PT: 3, St: "todo", Owner: "BA",
				Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
				Run: "idea", By: "BA"},
		},
	}); err != nil {
		return err
	}

	// qa：Agent 单元。制品树照样齐全——Agent 也要走五阶段。
	if err := seedPBC(ctx, log, "qa", "交付质检 Agent", pbcSpec{
		prdNote: "质检范围：话术合规 / 承诺一致性 / 情绪风险",
		msg:     "基线：质检 Agent 定义为可执行规格", digest: "sha256:4d7b18", tag: "v1.0.0",
	}); err != nil {
		return err
	}

	// dun：组合单元。服务侧验收 20/22 —— 故意不是全过：
	// 组合项最怕「服务验完就发」，一个全绿的面板演示不出来这件事。
	return seedPBC(ctx, log, "dun", "智能催缴", pbcSpec{
		prdNote: "催缴策略服务 + 催缴 Agent 组合交付：服务管什么时候催，Agent 管怎么说",
		msg:     "基线：催缴服务与催缴 Agent 一起交付", digest: "sha256:2f9c60", tag: "v2.0.0",
		svc: &domain.SvcVerify{Pass: 20, Total: 22, Fails: []domain.FailGroup{
			{N: "逾期 1-3 天误催", C: 1, Ex: "逾期 2 天仍进入催告模板"},
			{N: "频次控制边界", C: 1, Ex: "7 天内第 4 次触达未被拦截"},
		}},
	})
}

// pbcSpec 是一个 PBC 的种子内容。零值即「只有 pbc.created」。
type pbcSpec struct {
	prdNote string
	msg     string
	digest  string
	tag     string
	svc     *domain.SvcVerify
	items   []store.Item
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

func seedPBC(ctx context.Context, log *store.Store, pbc, name string, spec pbcSpec) error {
	ok, err := empty(ctx, log, pbc)
	if err != nil || !ok {
		return err
	}
	if _, err := log.Append(ctx, pbc, store.FactPBCCreated, "PA", map[string]string{"name": name}); err != nil {
		return err
	}
	if spec.msg == "" {
		return nil
	}
	if err := seedBaseline(ctx, log, pbc, spec.prdNote, spec.msg, spec.digest); err != nil {
		return err
	}
	if spec.svc != nil {
		if _, err := log.Append(ctx, pbc, store.FactSvcVerified, "x-qc", *spec.svc); err != nil {
			return err
		}
	}
	if spec.tag != "" {
		if err := seedTag(ctx, log, pbc, spec.tag); err != nil {
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
		if _, err := log.AppendOp(ctx, "units", op, store.FactUnitCreated, "PA", map[string]any{
			"id": u.ID, "name": u.Name, "code": u.Code, "kind": u.Kind, "agentId": u.AgentID,
		}); err != nil {
			return err
		}
	}
	for _, id := range removedUnits {
		if _, ok := have[id]; !ok {
			continue
		}
		if _, err := log.AppendOp(ctx, "units", "seed/v26/unit/removed/"+id, store.FactUnitRemoved, "PA",
			map[string]any{"id": id}); err != nil {
			return err
		}
	}
	if len(uv.Units) == 0 {
		return seedBRD(ctx, log)
	}
	brd, err := store.ProjectBRD(facts)
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
			if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/"+a.ID, store.FactAgentDefined, "PA", a); err != nil {
				return err
			}
		}
		// 种子里的 Agent 已经构建过：种子定义的 digest 就是 BuiltDigest。
		// 这一条决定了「digest 一致」闸在演示数据上是绿的。
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/"+a.ID+"/built", store.FactAgentBuilt, "DA",
			map[string]any{"agentId": a.ID, "ver": a.Ver, "digest": a.Digest}); err != nil {
			return err
		}
	}
	// v2.6 只有 X-运维工程师处于「已发布」（五阶段全 done）。
	// 老账本里 x-qa 曾以「质检」身份发布过：撤下它，避免一页上出现
	// 「流程是测试执行、发布记录是质检」的自相矛盾。
	if old := av.Agents["x-qa"]; old != nil && old.Published {
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-qa/retire", store.FactAgentRetired, "SA",
			map[string]any{"agentId": "x-qa"}); err != nil {
			return err
		}
	}
	if old := av.Agents["x-ops"]; old == nil || !old.Published {
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-ops/publish", store.FactAgentPublish, "SA",
			map[string]any{"agentId": "x-ops", "ver": "1.2.1", "digest": agentDigestOf(as, "x-ops"), "tag": "v1.2.1"}); err != nil {
			return err
		}
		if _, err := log.AppendOp(ctx, "agents", "seed/v26/agent/x-ops/rollout", store.FactAgentRollout, "DA",
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
		if _, err := log.AppendOp(ctx, "units", op, store.FactBRDSaved, "BA", map[string]any{
			"v": v.V, "at": v.At, "by": "BA", "st": v.St, "note": v.Note, "text": text,
		}); err != nil {
			return err
		}
	}
	return nil
}

// seedBaseline 写 v1.0.1 的制品线与基线提交。
//
// 为什么必须有它：交付链路的「引用闭合」闸要求树里六件制品齐备，且每件的
// 衍生引用都指向树里同一版本。树是从版本线取 latest 得来的，所以没有基线版本
// 的制品在树上就是空串 → 闸拒绝。
func seedBaseline(ctx context.Context, log *store.Store, pbc, prdNote, msg, digest string) error {
	const at = "11-20 17:20"
	vers := []domain.Version{
		{T: domain.PRD, V: "1.0.1", At: at, By: "BA", Source: "手动", St: domain.StAccepted, Note: prdNote},
		{T: domain.Proto, V: "1.0.1", At: at, By: "x-ui", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}}, Note: "三屏：列表 / 生成 / 对账"},
		{T: domain.AD, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.Proto, V: "1.0.1"}}, Note: "单库设计"},
		{T: domain.AT, V: "1.0.1", At: at, By: "x-qa", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.AD, V: "1.0.1"}}, Note: "验收用例"},
		{T: domain.Backlog, V: "1.0.1", At: at, By: "x-ba", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}}, Note: "story / task 拆分"},
		{T: domain.Code, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}, {T: domain.Backlog, V: "1.0.1"}}, Note: "基线代码"},
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
	c := domain.Commit{
		No: "1.0.1", Tree: tree, By: "张昆", At: at, Msg: msg, Type: "new",
		TreeSHA: domain.TreeSHA(tree), Digest: digest,
	}
	_, err := log.AppendOp(ctx, pbc, "seed/"+pbc+"/baseline/commit", store.FactCommitCreated, "i3-pipeline", c)
	return err
}

func seedTag(ctx context.Context, log *store.Store, pbc, tag string) error {
	_, err := log.AppendOp(ctx, pbc, "seed/"+pbc+"/tag", store.FactTagCreated, "SA",
		map[string]any{"tag": tag, "commit": "1.0.1"})
	return err
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
		if _, err := log.AppendOp(ctx, pbc, op, store.FactItemCreated, it.By, it); err != nil {
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
		if _, err := log.AppendOp(ctx, unit, "seed/v26/checks/"+unit, store.FactCheckDefined, "BA",
			map[string]any{"items": spec.Checks}); err != nil {
			return err
		}
		for _, d := range spec.Defects {
			if _, err := log.AppendOp(ctx, unit, "seed/v26/defect/"+d.ID, store.FactDefect, d.By,
				d); err != nil {
				return err
			}
		}
	}
	return nil
}
