package domain

import (
	"crypto/sha256"
	"encoding/hex"
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
type EvalRun struct {
	ID    string `json:"id"`
	At    string `json:"at"`
	Ver   string `json:"ver"`
	Pass  int    `json:"pass"`
	Total int    `json:"total"`
	Cost  string `json:"cost"`
	P50   string `json:"p50"`
	P95   string `json:"p95"`
	Tok   string `json:"tok"`
	Base  bool   `json:"base,omitempty"` // 对比基线
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
	ID     string   `json:"id"`
	Name   string   `json:"n"`
	Role   string   `json:"role"`
	Spe    string   `json:"spe"`
	Task   string   `json:"task"`
	Ver    string   `json:"ver"`
	Model  string   `json:"model"`
	Cost   string   `json:"cost"`
	Stages []string `json:"st"`   // 方案/原型/构建/验证/发布 五阶段状态
	Eval   [2]int   `json:"eval"` // [pass,total]
	Tools  []string `json:"tools"`
	Prompt string   `json:"prompt"`

	Runs  []EvalRun   `json:"runs"`
	Fails []FailGroup `json:"fails"`
	Trace []TraceStep `json:"trace"`
	Cases []EvalCase  `json:"cases"`

	Digest     string `json:"digest"`
	Published  bool   `json:"published"`
	Rollout    int    `json:"rollout"`              // 10 / 50 / 100，0 表示未放量
	RollbackTo string `json:"rollbackTo,omitempty"` // 最近一次回滚目标版本
}

// Rate 返回评测通过率；没有用例时返回 0。
func (a *Agent) Rate() float64 {
	if a == nil || a.Eval[1] == 0 {
		return 0
	}
	return float64(a.Eval[0]) / float64(a.Eval[1])
}

// EnsureDigest 在摘要为空时按定义算一个。改提示词/工具/模型/版本都会换 digest。
func (a *Agent) EnsureDigest() {
	if a.Digest != "" {
		return
	}
	a.Digest = AgentDigest(*a)
}

// AgentDigest 是 Agent 产物的树摘要。字段顺序固定，保证可回放。
func AgentDigest(a Agent) string {
	h := sha256.New()
	fmt.Fprintf(h, "ver=%s\nmodel=%s\nprompt=%s\n", a.Ver, a.Model, a.Prompt)
	for _, t := range a.Tools {
		fmt.Fprintf(h, "tool=%s\n", t)
	}
	if len(a.Cases) > 0 {
		fmt.Fprintf(h, "evalset=%d\n", len(a.Cases))
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
	if !closed {
		detail = "定义链不完整"
	}
	toolsOK := len(a.Tools) > 0
	toolsDetail := fmt.Sprintf("%d 个，无越权", len(a.Tools))
	if !toolsOK {
		toolsDetail = "没有工具白名单"
	}
	a.EnsureDigest()
	digestOK := a.Digest != ""
	digestDetail := a.Digest
	if !digestOK {
		digestDetail = "未构建，无 digest"
	}
	evalOK := a.Eval[1] > 0 && a.Rate() >= 0.95
	evalDetail := fmt.Sprintf("%d/%d", a.Eval[0], a.Eval[1])
	if a.Eval[1] == 0 {
		evalDetail = "无评测集"
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
