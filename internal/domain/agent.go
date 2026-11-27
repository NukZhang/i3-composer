package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// UnitKind 是交付单元的三种构成（原型 v2.3）：
//   - pbc    只有微服务
//   - agent  只有 Agent 定义 + 评测集
//   - hybrid 微服务 + Agent，一次 tag 锁两条产物
type UnitKind string

const (
	KindPBC    UnitKind = "pbc"
	KindAgent  UnitKind = "agent"
	KindHybrid UnitKind = "hybrid"
)

// ParseKind 把外部输入收敛成合法 kind；空值默认 pbc（兼容 v2.1 数据）。
func ParseKind(s string) UnitKind {
	switch UnitKind(s) {
	case KindAgent:
		return KindAgent
	case KindHybrid:
		return KindHybrid
	default:
		return KindPBC
	}
}

func (k UnitKind) Valid() bool {
	return k == KindPBC || k == KindAgent || k == KindHybrid
}

func (k UnitKind) String() string { return string(k) }

// Label 是展示名，与原型 kindText 一致。
func (k UnitKind) Label() string {
	switch k {
	case KindAgent:
		return "Agent"
	case KindHybrid:
		return "PBC + Agent"
	default:
		return "PBC"
	}
}

// EvalRun 是一次评测运行的摘要。通过率是 Agent 的发布判据。
//
// Digest 记的是「这次评测跑在哪一版定义上」：改过编排/提示词但没重跑，
// 分数就不能拿来给新版本背书。
type EvalRun struct {
	ID     string `json:"id"`
	At     string `json:"at"`
	Ver    string `json:"ver"`
	Pass   int    `json:"pass"`
	Total  int    `json:"total"`
	Cost   string `json:"cost"`
	P50    string `json:"p50"`
	P95    string `json:"p95"`
	Tok    string `json:"tok"`
	Base   bool   `json:"base,omitempty"`   // 对比基线
	Digest string `json:"digest,omitempty"` // 评测所针对的定义 digest
}

// EvalCase 是评测集里的一条用例（原型 v2.3 的 evCases）。
type EvalCase struct {
	V     string  `json:"v"`
	In    string  `json:"in"`
	Out   string  `json:"out"`
	Exp   string  `json:"exp"`
	Why   string  `json:"why"`
	Ms    string  `json:"ms"`
	Tok   string  `json:"tok"`
	Score float64 `json:"score"`
	OK    bool    `json:"ok"`
	// Assemblies 是沉淀这条用例时那一轮的上下文装配结果。
	//
	// 评测说「这条失败」时，光有输入输出回答不了「是不是没召回」；
	// 把当轮装配钉在用例上，点开失败详情就能看到这一轮到底载入了什么、
	// 漏了多少——而不是去查最新一轮（那条常常已经不是失败的那次了）。
	Assemblies []ContextAssembly `json:"assemblies,omitempty"`
}

// FailGroup 是失败聚类：同一原因归一组。
type FailGroup struct {
	N  string `json:"n"`
	C  int    `json:"c"`
	Ex string `json:"ex"`
}

// TraceStep 是一条执行轨迹（评测展开时展示）。
type TraceStep struct {
	N   string `json:"n"`
	D   string `json:"d"`
	Ms  int    `json:"ms"`
	Tok int    `json:"tok"`
}

// Agent 是一个数字员工的定义与版本状态。
//
// 它不是「配置项」：提示词、工具白名单、模型、评测集共同决定 digest，
// 任何一项变化都是新版本，和 PBC 的提交模型一致。
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"n"`
	Role string `json:"role"`
	// Product 区分两种东西：false = 平台员工（写代码、跑测试、画原型的执行者），
	// true = 交付给客户的 Agent 产物（随单元走四阶段、有评测集、能打 tag）。
	// 把两者混在一个花名册里，就会出现「给测试工程师配版本号」这种概念错误。
	Product bool     `json:"product,omitempty"`
	Spe     string   `json:"spe"`
	Task    string   `json:"task"`
	Ver     string   `json:"ver"`
	Model   string   `json:"model"`
	Cost    string   `json:"cost"`
	Stages  []string `json:"st"`   // 方案/原型/构建/验证/发布 五阶段状态
	Eval    [2]int   `json:"eval"` // [pass,total]
	Tools   []string `json:"tools"`
	Prompt  string   `json:"prompt"`

	Runs  []EvalRun   `json:"runs"`
	Fails []FailGroup `json:"fails"`
	Trace []TraceStep `json:"trace"`
	Cases []EvalCase  `json:"cases"`

	// v2.6：Agent 的构建产物是一张编排，不是一份 YAML。
	// Flow 决定它在什么情况下查库、调工具、找人确认；KB 是它引用的知识库
	// （库版本进 digest）；Trial 是原型阶段「试跑这一条」的样例。
	Flow     *Flow  `json:"flow,omitempty"`
	KB       *KB    `json:"kb,omitempty"`
	Trial    *Trial `json:"trial,omitempty"`
	TrialRun *Trial `json:"trialRun,omitempty"`
	TrialAt  string `json:"trialAt,omitempty"`
	TrialBy  string `json:"trialBy,omitempty"`
	// LastLoadedTrial 是**最近一次真的载入了知识**的试跑（任一 kb 节点 loaded>0），
	// 连同当轮 assemblies 一起。TrialRun 只记最后一次，而最后一次可能是被白名单
	// 拦掉的一轮（kb 的 query 为空、loaded=0）——「上一次检索到底召回没召回」这类
	// 问题不该依赖「最后一次恰好也是成功的那次」这种运气（#33）。
	LastLoadedTrial *Trial `json:"lastLoadedTrial,omitempty"`
	LastLoadedAt    string `json:"lastLoadedAt,omitempty"`

	Digest string `json:"digest"`
	// BuiltDigest 是最后一次「构建」落下的 digest。它和 Digest 不一致，
	// 说明编排改了但没构建——gate 会红，上一轮评测也不能再给新版本背书。
	BuiltDigest string `json:"builtDigest,omitempty"`

	Published  bool   `json:"published"`
	Rollout    int    `json:"rollout"`              // 10 / 50 / 100，0 表示未放量
	RollbackTo string `json:"rollbackTo,omitempty"` // 最近一次回滚目标版本
}

// Allows 判定一个工具在不在白名单里。执行器用它拦越界调用。
//
// 白名单为空时一律不许：那不是「不限制」，是「没给权限」。
func (a *Agent) Allows(tool string) bool {
	if a == nil || tool == "" {
		return false
	}
	for _, t := range a.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// Rate 返回评测通过率；没有用例时返回 0。
func (a *Agent) Rate() float64 {
	if a == nil || a.Eval[1] == 0 {
		return 0
	}
	return float64(a.Eval[0]) / float64(a.Eval[1])
}

// EnsureDigest 按当前定义重算 digest。改节点、提示词、工具、模型或库版本都会变。
func (a *Agent) EnsureDigest() {
	a.Digest = AgentDigest(*a)
}

// LatestRun 返回最近一次评测运行；没有则返回 nil。
func (a *Agent) LatestRun() *EvalRun {
	if a == nil || len(a.Runs) == 0 {
		return nil
	}
	return &a.Runs[0]
}

// AgentDigest 是 Agent 产物的摘要。字段顺序固定，保证可回放。
//
// 刻意不包含版本号：版本是 digest 变化的结果，不是它的输入；
// 包含它会变成「构建一次变一次」，永远对不上。
func AgentDigest(a Agent) string {
	h := sha256.New()
	fmt.Fprintf(h, "model=%s\nprompt=%s\n", a.Model, a.Prompt)
	for _, t := range a.Tools {
		fmt.Fprintf(h, "tool=%s\n", t)
	}
	if len(a.Cases) > 0 {
		fmt.Fprintf(h, "evalset=%d\n", len(a.Cases))
	}
	if a.Flow != nil {
		b, _ := json.Marshal(a.Flow)
		fmt.Fprintf(h, "flow=%s\n", b)
	}
	if kb := a.KB.Ref(); kb != "" {
		fmt.Fprintf(h, "kb=%s\n", kb)
		fmt.Fprintf(h, "kbcfg=%d/%.3f/%t\n", a.KB.TopK, a.KB.Thr, a.KB.Rerank)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:12]
}

// SvcVerify 是组合交付单元里服务侧的验收结果（原型 dun.svcVerify）。
type SvcVerify struct {
	Pass  int         `json:"pass"`
	Total int         `json:"total"`
	Fails []FailGroup `json:"fails"`
}

// OK 判定服务验收是否全过。0/0 不算通过——没验过不等于通过。
func (s SvcVerify) OK() bool {
	return s.Total > 0 && s.Pass == s.Total
}

// AgentGates 是 Agent 的四闸：上游闭合 / 工具白名单 / digest 一致 / 评测达标。
//
// 「上游闭合」在 Agent 语境下指定义链完整：有版本、有提示词、五阶段有位置。
// 「评测达标」是 95% 通过率，和原型 renderVerifyAgent 完全一致。
func AgentGates(a *Agent) []Gate {
	if a == nil {
		return []Gate{
			{Key: "agent-closure", Label: "上游闭合", OK: false, Detail: "未定义 Agent"},
			{Key: "agent-tools", Label: "工具白名单", OK: false, Detail: "无工具"},
			{Key: "agent-digest", Label: "digest 一致", OK: false, Detail: "未构建"},
			{Key: "agent-eval", Label: "评测达标", OK: false, Detail: "无评测"},
		}
	}
	closed := a.Ver != "" && a.Prompt != "" && len(a.Stages) == 5
	detail := "方案 / 原型 / 构建 / 验证 / 发布 齐"
	if closed && a.Flow != nil {
		if err := a.Flow.Valid(); err != nil {
			closed = false
			detail = "编排不可执行：" + err.Error()
		} else {
			detail += fmt.Sprintf(" · %d 节点 / %d 连线", len(a.Flow.Nodes), len(a.Flow.Edges))
		}
	}
	if !closed && detail == "方案 / 原型 / 构建 / 验证 / 发布 齐" {
		detail = "定义链不完整"
	}
	// 「无越权」必须是算出来的，不能因为「名单非空」就说。编排里用了名单外的
	// 工具，执行器照样会跑（它只认工具名）——这条闸以前只看了名单非空，
	// 于是把「没核对」说成了「没问题」。
	toolsOK := len(a.Tools) > 0
	toolsDetail := fmt.Sprintf("%d 个，无越权", len(a.Tools))
	// 白名单为空时下面必然没有越界项（一个都不允许），但那条红要单独说清楚：
	// 「没给名单」和「给了名单但有越界」是两种毛病，修法不同。
	if !toolsOK {
		toolsDetail = "没有工具白名单"
	} else if a.Flow != nil {
		if outside := a.Flow.ToolsOutside(a.Tools); len(outside) > 0 {
			toolsOK = false
			toolsDetail = DescribeOutside(outside)
		}
	}
	a.EnsureDigest()
	digestOK := a.BuiltDigest != "" && a.BuiltDigest == a.Digest
	digestDetail := a.BuiltDigest
	if a.BuiltDigest == "" {
		digestDetail = "未构建，无 digest"
	} else if !digestOK {
		digestDetail = "编排改了但没构建（当前 " + a.Digest + "）"
	}
	evalOK := a.Eval[1] > 0 && a.Rate() >= 0.95
	evalDetail := fmt.Sprintf("%d/%d", a.Eval[0], a.Eval[1])
	if a.Eval[1] == 0 {
		evalDetail = "无评测集"
	} else if run := a.LatestRun(); run != nil && run.Digest != "" && a.BuiltDigest != "" && run.Digest != a.BuiltDigest {
		evalOK = false
		evalDetail += " · 评测针对旧版本，需重跑"
	}
	return []Gate{
		{Key: "agent-closure", Label: "上游闭合", OK: closed, Detail: detail},
		{Key: "agent-tools", Label: "工具白名单", OK: toolsOK, Detail: toolsDetail},
		{Key: "agent-digest", Label: "digest 一致", OK: digestOK, Detail: digestDetail},
		{Key: "agent-eval", Label: "评测达标", OK: evalOK, Detail: evalDetail},
	}
}

// HybridGates 把服务侧与 Agent 侧的判据合成一个发布结论。
// 返回顺序：服务 4 闸 → Agent 4 闸。调用方按 len/2 切分展示。
func HybridGates(pbc []Gate, sv SvcVerify, ag []Gate) []Gate {
	out := make([]Gate, 0, len(pbc)+len(ag)+1)
	out = append(out, pbc...)
	out = append(out, Gate{
		Key: "svc-verify", Label: "服务验收全过", OK: sv.OK(),
		Detail: fmt.Sprintf("%d/%d", sv.Pass, sv.Total),
	})
	out = append(out, ag...)
	return out
}

// HybridCanRelease 判定组合单元能否打 tag：两侧全过才允许。
// 注意服务侧用 svc-verify 替换了「原型还原」，因为组合单元的服务侧看验收清单。
func HybridCanRelease(gs []Gate) bool { return GatesPass(gs) }
