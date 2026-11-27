package store

import (
	"encoding/json"
	"fmt"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// 本文件是 v2.3 新增的两条账本线：
//
//   - units 日志：交付单元的构成（pbc / agent / hybrid）与项目级 BRD。
//     单元属于「项目」而不是某次交付，所以单独一条日志。
//   - agents 日志：数字员工的定义、评测运行、发布与灰度。
//     Agent 是可复用资产，不属于某个 PBC；某个交付单元只是引用它的 id。
//
// 两条线都遵守同一套规则：只追加、可重放、投影是纯函数。

// Unit 是一个交付单元的登记信息。进度（head/tags）由各自的 PBC 日志给出。
type Unit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Kind    string `json:"kind"`
	AgentID string `json:"agentId,omitempty"`
	Profile string `json:"profile,omitempty"`
	// Proj 是所属项目（v2.7 起交付单元挂在项目下，不再是全局列表）。
	// 老账本里没有这个字段的单元归入 DefaultProject。
	Proj string `json:"proj,omitempty"`
	// RegistrySeq 绑定作废请求与这一版登记，避免延迟写入误伤重登后的单元。
	RegistrySeq  int  `json:"registrySeq,omitempty"`
	Reregistered bool `json:"-"`
}

// UnitsView 是 units 日志的投影。
type UnitsView struct {
	Seq     int             `json:"seq"`
	Units   []Unit          `json:"units"`
	Removed map[string]Unit `json:"-"`
}

// BRDVersion 是项目级 BRD 的一次保存。
type BRDVersion struct {
	V    string `json:"v"`
	At   string `json:"at"`
	By   string `json:"by"`
	St   string `json:"st"`
	Note string `json:"note"`
	Text string `json:"text,omitempty"`
}

// BRDView 是 BRD 的版本线。Text 只在最新一版上返回，避免每次全量。
type BRDView struct {
	V     string       `json:"v"`
	St    string       `json:"st"`
	At    string       `json:"at"`
	By    string       `json:"by"`
	Text  string       `json:"text"`
	Vers  []BRDVersion `json:"versions"`
	Words int          `json:"words"`
}

// AgentsView 是 agents 日志的投影。
type AgentsView struct {
	Seq    int                      `json:"seq"`
	Agents map[string]*domain.Agent `json:"agents"`
	Order  []string                 `json:"order"` // 首次定义的顺序，前端名片按此排列
}

func ProjectUnits(facts []Fact) (*UnitsView, error) {
	v := &UnitsView{Seq: len(facts), Removed: map[string]Unit{}}
	seen := map[string]bool{}
	for _, f := range facts {
		switch f.Type {
		case FactUnitCreated:
			var u Unit
			if err := json.Unmarshal(f.Payload, &u); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if u.ID == "" {
				return nil, fmt.Errorf("unit 事缺少 id")
			}
			if u.Proj == "" {
				u.Proj = DefaultProject
			}
			u.Kind = domain.ParseKind(u.Kind).String()
			u.RegistrySeq = f.Seq
			if seen[u.ID] {
				for i := range v.Units {
					if v.Units[i].ID == u.ID {
						u.Reregistered = v.Units[i].Reregistered
						v.Units[i] = u
					}
				}
				continue
			}
			seen[u.ID] = true
			_, u.Reregistered = v.Removed[u.ID]
			delete(v.Removed, u.ID)
			v.Units = append(v.Units, u)

		case FactUnitRemoved:
			var p struct {
				ID         string `json:"id"`
				CreatedSeq int    `json:"createdSeq"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			for i := range v.Units {
				// 旧账本的作废事实没有登记序号，仍按 ID 重放。
				if v.Units[i].ID == p.ID && (p.CreatedSeq == 0 || v.Units[i].RegistrySeq == p.CreatedSeq) {
					v.Removed[p.ID] = v.Units[i]
					v.Units = append(v.Units[:i], v.Units[i+1:]...)
					delete(seen, p.ID)
					break
				}
			}
		}
	}
	return v, nil
}

// ProjectBRD 重放某个项目的 BRD 版本线。
//
// v2.7 起 BRD 挂在项目上：同一条 units 日志里按 payload 的 proj 过滤。
// 老账本里的 BRD 事实没有 proj 字段，归入 DefaultProject。
func ProjectBRD(facts []Fact, proj string) (*BRDView, error) {
	if proj == "" {
		proj = DefaultProject
	}
	v := &BRDView{}
	for _, f := range facts {
		if f.Type != FactBRDSaved {
			continue
		}
		var p struct {
			Proj string `json:"proj"`
			V    string `json:"v"`
			At   string `json:"at"`
			By   string `json:"by"`
			St   string `json:"st"`
			Note string `json:"note"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
		}
		// 没写 proj 的老事实归 DefaultProject：升级不丢历史。
		if p.Proj == "" {
			p.Proj = DefaultProject
		}
		if p.Proj != proj {
			continue
		}
		// 新一版草稿落账时，上一版草稿变为 superseded：和制品版本线同一规则。
		if p.St == "" {
			p.St = "draft"
		}
		if p.St == "draft" {
			for i := range v.Vers {
				if v.Vers[i].St == "draft" {
					v.Vers[i].St = "superseded"
				}
			}
		}
		v.Vers = append(v.Vers, BRDVersion{V: p.V, At: p.At, By: p.By, St: p.St, Note: p.Note, Text: p.Text})
		if p.Text != "" {
			v.Text = p.Text
			v.Words = len([]rune(p.Text))
		}
		v.V, v.St, v.At, v.By = p.V, p.St, p.At, p.By
	}
	return v, nil
}

func ProjectAgents(facts []Fact) (*AgentsView, error) {
	v := &AgentsView{Seq: len(facts), Agents: map[string]*domain.Agent{}}
	for _, f := range facts {
		switch f.Type {
		case FactAgentDefined:
			var a domain.Agent
			if err := json.Unmarshal(f.Payload, &a); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a.ID == "" {
				return nil, fmt.Errorf("agent 事缺少 id")
			}
			// 定义升级（例如原型 v2.6 换了编排）只覆盖定义本身，
			// 不抹掉已经发生的发布/灰度/构建：那些是事件，不是配置。
			if old := v.Agents[a.ID]; old != nil {
				a.Published, a.Rollout, a.RollbackTo = old.Published, old.Rollout, old.RollbackTo
				if a.BuiltDigest == "" {
					a.BuiltDigest = old.BuiltDigest
				}
			}
			a.EnsureDigest()
			if _, ok := v.Agents[a.ID]; !ok {
				v.Order = append(v.Order, a.ID)
			}
			v.Agents[a.ID] = &a

		case FactAgentEvalRun:
			var p struct {
				AgentID string         `json:"agentId"`
				Run     domain.EvalRun `json:"run"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			a := v.Agents[p.AgentID]
			if a == nil {
				continue
			}
			// 新的在前：评测台默认拿 runs[0] 作为本轮。
			a.Runs = append([]domain.EvalRun{p.Run}, a.Runs...)
			if p.Run.Total > 0 {
				a.Eval = [2]int{p.Run.Pass, p.Run.Total}
			}
			// 版本只前进不后退：构建定版本，评测只报告「评的是哪一版」。
			// 拿旧版本的评测结果回来，不该把刚构建出来的版本号退回去。
			if p.Run.Ver != "" && verNewer(p.Run.Ver, a.Ver) {
				a.Ver = p.Run.Ver
			}
			a.EnsureDigest()

		case FactAgentPublish:
			var p struct {
				AgentID string `json:"agentId"`
				Ver     string `json:"ver"`
				Digest  string `json:"digest"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil {
				if p.Ver != "" {
					a.Ver = p.Ver
				}
				if p.Digest != "" {
					a.Digest = p.Digest
				}
				a.Published = true
				a.Rollout = 10 // 发布即第一档，不许直接全量
				a.RollbackTo = ""
			}

		case FactAgentRollout:
			var p struct {
				AgentID string `json:"agentId"`
				Pct     int    `json:"pct"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil && validRollout(p.Pct) {
				a.Rollout = p.Pct
			}

		case FactAgentRollback:
			var p struct {
				AgentID string `json:"agentId"`
				To      string `json:"to"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil {
				a.Rollout = 0
				a.RollbackTo = p.To
			}

		case FactAgentBaseline:
			var p struct {
				AgentID string `json:"agentId"`
				RunID   string `json:"runId"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			// 基线只允许一条：设新的就是换掉旧的，不做「多个基线」。
			if a := v.Agents[p.AgentID]; a != nil {
				for i := range a.Runs {
					a.Runs[i].Base = a.Runs[i].ID == p.RunID
				}
			}

		case FactAgentFlowSaved:
			var p struct {
				AgentID string       `json:"agentId"`
				Flow    *domain.Flow `json:"flow"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil && p.Flow != nil {
				// 编排进定义，digest 立即变；BuiltDigest 留在上一次构建上，
				// 于是「改了没构建」这件事在闸门里是可见的。
				a.Flow = p.Flow
				a.EnsureDigest()
			}

		case FactAgentBuilt:
			var p struct {
				AgentID string `json:"agentId"`
				Ver     string `json:"ver"`
				Digest  string `json:"digest"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil {
				if p.Ver != "" {
					a.Ver = p.Ver
				}
				a.BuiltDigest = p.Digest
				a.EnsureDigest()
			}

		case FactAgentTrial:
			var p struct {
				AgentID string        `json:"agentId"`
				Trial   *domain.Trial `json:"trial"`
				At      string        `json:"at"`
				By      string        `json:"by"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil && p.Trial != nil {
				a.TrialRun = p.Trial
				a.TrialAt, a.TrialBy = p.At, p.By
				// 只有真载入了知识的那一轮才更新 lastLoadedTrial：被工具白名单拦掉的一轮
				// kb 拿到的 query 是空的、loaded=0，它不是「最近一次检索的结果」（#33）。
				if trialRetrieved(p.Trial) {
					a.LastLoadedTrial = p.Trial
					a.LastLoadedAt = p.At
				}
			}

		case FactAgentCase:
			var p struct {
				AgentID string          `json:"agentId"`
				Case    domain.EvalCase `json:"case"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil {
				a.Cases = append(a.Cases, p.Case)
				// 评测集变了，digest 也变：新用例必须先被评测过才算数。
				a.EnsureDigest()
			}

		case FactKBIssue:
			var p struct {
				AgentID string `json:"agentId"`
				Idx     int    `json:"idx"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil && a.KB != nil && p.Idx >= 0 && p.Idx < len(a.KB.Issues) {
				a.KB.Issues[p.Idx].Dispatched = true
			}

		case FactAgentRetired:
			// 撤下某个已发布的版本：流量归零、不再作为在跑版本。
			var p struct {
				AgentID string `json:"agentId"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if a := v.Agents[p.AgentID]; a != nil {
				a.Published = false
				a.Rollout = 0
				a.RollbackTo = ""
			}
		}
	}
	return v, nil
}

// trialRetrieved 判定这一轮试跑是否真的检索过并载入了知识：
// 至少一条装配有 query、且载入数 > 0。被工具白名单拦掉的一轮，kb 的输入没进来，
// query 空、loaded 0——它是证据，但不是「最近一次检索的结果」。
func trialRetrieved(t *domain.Trial) bool {
	for _, a := range t.Assemblies {
		if a.Query != "" && len(a.Loaded) > 0 {
			return true
		}
	}
	return false
}

func validRollout(p int) bool { return p == 10 || p == 50 || p == 100 }

// verNewer 判断版本 a 是否比 b 新（major.minor.patch 逐段比）。
// 无法解析时一律返回 false：宁可不动版本，也不要把版本号写坏。
func verNewer(a, b string) bool {
	var am, an, ap, bm, bn, bp int
	if _, err := fmt.Sscanf(a, "%d.%d.%d", &am, &an, &ap); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(b, "%d.%d.%d", &bm, &bn, &bp); err != nil {
		return false
	}
	if am != bm {
		return am > bm
	}
	if an != bn {
		return an > bn
	}
	return ap > bp
}
