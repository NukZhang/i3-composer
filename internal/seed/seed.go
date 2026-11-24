// Package seed 提供可演示的起点数据。
//
// 数据本身来自原型 v2.3 的 AGENT_META / WORKERS（见 docs/prototypes/），
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

// Agents 解出 6 名数字员工的定义。返回的是值拷贝，调用方可以随便改。
func Agents() ([]domain.Agent, error) {
	var out []domain.Agent
	if err := json.Unmarshal(agentsJSON, &out); err != nil {
		return nil, fmt.Errorf("解析 seed/agents.json 失败：%w", err)
	}
	for i := range out {
		out[i].EnsureDigest()
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

// Units 对应原型 DB.pbcs 的四个单元：fee（纯 PBC）、ticket（纯 PBC）、
// qa（Agent）、dun（hybrid）。
func Units() []Unit {
	return []Unit{
		{ID: "fee", Name: "收费缴费", Code: "PBC-FEE", Kind: "pbc"},
		{ID: "ticket", Name: "客服工单", Code: "PBC-TKT", Kind: "pbc"},
		{ID: "qa", Name: "交付质检 Agent", Code: "AGENT-QA", Kind: "agent", AgentID: "x-qa"},
		{ID: "dun", Name: "智能催缴", Code: "PBC-DUN + AGENT-DUN", Kind: "hybrid", AgentID: "x-dun"},
	}
}

// Run 写演示起点。可重复执行：每条日志各自判空，已存在的日志原样保留。
//
// 为什么按日志判空而不是「整体判空」：v2.1 时期只种了 fee，v2.3 新增了
// units / agents 两条日志。整体判空会让老环境的账本永远补不上单元与 Agent，
// 而按日志判空既能补新，又不会给已有数据重复种。
func Run(ctx context.Context, log *store.Store) error {
	// 项目级资产：单元、BRD、Agent 是 PBC 引用的目标，先落。
	if err := seedProject(ctx, log); err != nil {
		return err
	}
	if err := seedAgents(ctx, log); err != nil {
		return err
	}

	// fee：完整的制品基线 + 三条工作项（与 v2.1 的种子一致）。
	if err := seedPBC(ctx, log, "fee", "悦府物业 · 收费与工单", pbcSpec{
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

	// ticket：另一个纯 PBC，只有基线。
	if err := seedPBC(ctx, log, "ticket", "客服工单", pbcSpec{
		prdNote: "报修受理与派单", msg: "基线：工单 PBC 初始化",
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
		if _, err := log.Append(ctx, pbc, store.FactSvcVerified, "x-qa", *spec.svc); err != nil {
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
func seedProject(ctx context.Context, log *store.Store) error {
	ok, err := empty(ctx, log, "units")
	if err != nil || !ok {
		return err
	}
	for _, u := range Units() {
		op := "seed/unit/" + u.ID
		if _, err := log.AppendOp(ctx, "units", op, store.FactUnitCreated, "PA", map[string]any{
			"id": u.ID, "name": u.Name, "code": u.Code, "kind": u.Kind, "agentId": u.AgentID,
		}); err != nil {
			return err
		}
	}
	return seedBRD(ctx, log)
}

func seedAgents(ctx context.Context, log *store.Store) error {
	ok, err := empty(ctx, log, "agents")
	if err != nil || !ok {
		return err
	}
	as, err := Agents()
	if err != nil {
		return err
	}
	for _, a := range as {
		op := "seed/agent/" + a.ID
		if _, err := log.AppendOp(ctx, "agents", op, store.FactAgentDefined, "PA", a); err != nil {
			return err
		}
		// x-qa 与 x-ops 已经在产线上跑过一轮且全过，对应原型的「已发布」。
		if a.ID == "x-qa" || a.ID == "x-ops" {
			if _, err := log.AppendOp(ctx, "agents", op+"/published", store.FactAgentPublish, "SA",
				map[string]any{"agentId": a.ID, "ver": a.Ver, "digest": a.Digest, "tag": "v" + a.Ver}); err != nil {
				return err
			}
			if _, err := log.AppendOp(ctx, "agents", op+"/rollout", store.FactAgentRollout, "DA",
				map[string]any{"agentId": a.ID, "pct": 100}); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedBRD 是原型 BRD_MD 的起点：v1.2 草稿，§4 待补。
// 只在 units 日志刚建好时调用（见 seedProject）。
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
