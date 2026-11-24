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

// Run 写演示起点。可重复执行：每段都先看日志是否为空。
func Run(ctx context.Context, log *store.Store) error {
	if err := log.Ensure(ctx, "fee"); err != nil {
		return err
	}
	facts, err := log.Load(ctx, "fee")
	if err != nil {
		return err
	}
	if len(facts) > 0 {
		return nil
	}

	// 单元与 Agent 是项目级资产：先落它们，PBC 自己的日志才有东西可引用。
	if err := seedUnits(ctx, log); err != nil {
		return err
	}
	if err := seedAgents(ctx, log); err != nil {
		return err
	}
	if err := seedBRD(ctx, log); err != nil {
		return err
	}

	// fee：完整的制品基线 + 三条工作项（与 v2.1 的种子一致）。
	if _, err := log.Append(ctx, "fee", store.FactPBCCreated, "PA", map[string]string{"name": "悦府物业 · 收费与工单"}); err != nil {
		return err
	}
	if err := seedBaseline(ctx, log, "fee", "收费与工单基线", "基线：PRD 手动上传 + 三件制品生成", "sha256:baseline"); err != nil {
		return err
	}
	items := []store.Item{
		{ID: "US-01", Title: "业主账单自助查询与支付", PT: 5, St: "done", Owner: "DA",
			Kind: "Story", PBC: "fee", Type: "new", Mode: "guided", Env: "test",
			Run: "published", Version: "1.0.2", Tag: "v1.0.0", By: "BA"},
		{ID: "US-02", Title: "管家批量生成缴费单", PT: 3, St: "doing", Owner: "DA",
			Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
			Run: "waiting", Step: 6, By: "PA"},
		{ID: "US-03", Title: "报修工单 SLA 超时提醒", PT: 3, St: "todo", Owner: "BA",
			Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
			Run: "idea", By: "BA"},
	}
	if err := seedItems(ctx, log, "fee", items); err != nil {
		return err
	}

	// ticket：另一个纯 PBC，只有基线。
	if _, err := log.Append(ctx, "ticket", store.FactPBCCreated, "PA", map[string]string{"name": "客服工单"}); err != nil {
		return err
	}
	if err := seedBaseline(ctx, log, "ticket", "报修受理与派单", "基线：工单 PBC 初始化", ""); err != nil {
		return err
	}

	// qa：Agent 单元。制品树照样齐全——Agent 也要走五阶段。
	if _, err := log.Append(ctx, "qa", store.FactPBCCreated, "PA", map[string]string{"name": "交付质检 Agent"}); err != nil {
		return err
	}
	if err := seedBaseline(ctx, log, "qa", "质检范围：话术合规 / 承诺一致性 / 情绪风险",
		"基线：质检 Agent 定义为可执行规格", "sha256:4d7b18"); err != nil {
		return err
	}
	if err := seedTag(ctx, log, "qa", "v1.0.0"); err != nil {
		return err
	}

	// dun：组合单元。服务侧验收 20/22 —— 故意不是全过：
	// 组合项最怕「服务验完就发」，一个全绿的面板演示不出来这件事。
	if _, err := log.Append(ctx, "dun", store.FactPBCCreated, "PA", map[string]string{"name": "智能催缴"}); err != nil {
		return err
	}
	if err := seedBaseline(ctx, log, "dun",
		"催缴策略服务 + 催缴 Agent 组合交付：服务管什么时候催，Agent 管怎么说",
		"基线：催缴服务与催缴 Agent 一起交付", "sha256:2f9c60"); err != nil {
		return err
	}
	if _, err := log.Append(ctx, "dun", store.FactSvcVerified, "x-qa", domain.SvcVerify{
		Pass: 20, Total: 22,
		Fails: []domain.FailGroup{
			{N: "逾期 1-3 天误催", C: 1, Ex: "逾期 2 天仍进入催告模板"},
			{N: "频次控制边界", C: 1, Ex: "7 天内第 4 次触达未被拦截"},
		},
	}); err != nil {
		return err
	}
	if err := seedTag(ctx, log, "dun", "v2.0.0"); err != nil {
		return err
	}
	return nil
}

func seedUnits(ctx context.Context, log *store.Store) error {
	for _, u := range Units() {
		op := "seed/unit/" + u.ID
		if _, err := log.AppendOp(ctx, "units", op, store.FactUnitCreated, "PA", map[string]any{
			"id": u.ID, "name": u.Name, "code": u.Code, "kind": u.Kind, "agentId": u.AgentID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func seedAgents(ctx context.Context, log *store.Store) error {
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
