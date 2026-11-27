// Package wf 是 L3 的编排层：Dapr Workflow 定义「做什么、什么顺序」，
// activity 才是真正干活的地方。
//
// 一条铁律：workflow 函数里不读时钟、不读环境、不碰 IO——
// 它会被重放（replay），任何非确定性都会让回放与原始执行分叉。
// 所有副作用（写事实、调外部系统）只允许出现在 activity 里。
package wf

import (
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// UnitInfo 是一个交付单元的登记信息（名字与构成）。
//
// 生成制品时需要它：代码制品长什么样取决于单元是 PBC（可编译的服务）
// 还是 Agent（编排定义）。
type UnitInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	AgentID string `json:"agentId,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// Input 是一个工作项进入 workflow 时的全部输入。
type Input struct {
	ItemID string `json:"itemId"`
	PBC    string `json:"pbc"`
	Title  string `json:"title"`
	Type   string `json:"type"` // new | feature | defect | backlog | doc
	Env    string `json:"env"`
	Mode   string `json:"mode"` // fast | guided
	// Ledger 是这本账的前缀。
	//
	// 为什么它要跟着工作项走：同一套 Dapr 上可能同时跑着几个进程
	// （同一 app-id 的多个副本、或隔离环境）。工作项落到哪个进程执行
	// 是 Dapr 决定的，进程不能自己决定「我用的是哪本账」。
	// 把前缀放进输入，activity 就永远写进工作项该去的那本账。
	Ledger string `json:"ledger,omitempty"`
}

// StepResult 是单个 activity 的结果。只回报「成没成」+ 一句说明；
// 真正的状态在事实日志里，不靠 workflow history 传递。
type StepResult struct {
	OK     bool   `json:"ok"`
	Step   int    `json:"step"`
	Detail string `json:"detail"`
	// Pass/Total 仅测试步使用，是 go test 的真实结果。
	Pass  int `json:"pass,omitempty"`
	Total int `json:"total,omitempty"`
	// CommitNo / Tag 由提交、打标两步回传。
	CommitNo string `json:"commitNo,omitempty"`
	Tag      string `json:"tag,omitempty"`
	URL      string `json:"url,omitempty"`
}

// BuildOut 是构建产物。
type BuildOut struct {
	Digest string `json:"digest"` // 内容寻址：sha256:<hex>
	Size   int64  `json:"size"`   // 字节数
	Binary bool   `json:"binary"` // 是否产出了可执行文件
	Detail string `json:"detail,omitempty"`
}

// TestOut 是测试的真实结果。
type TestOut struct {
	Pass     int      `json:"pass"`
	Total    int      `json:"total"`
	Failures []string `json:"failures,omitempty"`
	Output   string   `json:"output,omitempty"`
}

// DeployResult 是部署的真实结果。
type DeployResult struct {
	URL    string `json:"url"`
	PID    int    `json:"pid,omitempty"`
	Health string `json:"health,omitempty"`
	// Skipped 为真表示这个产物没有独立进程可部署（如 Agent 定义本身）。
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// workflow / activity 名称。名字是 Dapr 侧的唯一契约，改了就断历史。
const (
	NameDelivery = "i3-delivery"

	ActGenArtifacts = "i3-gen-artifacts"
	ActConverge     = "i3-converge"
	ActCommit       = "i3-commit"
	ActTest         = "i3-test"
	ActBuild        = "i3-build"
	ActDeploy       = "i3-deploy"
	ActPublish      = "i3-publish"
	ActWaiting      = "i3-mark-waiting"
	ActBlocked      = "i3-mark-blocked"
	// ActVerified 是 v3.2 的收口：机器五步跑完，记「可以交付了」。
	// 打 tag 不在这里——它是人在验证页按下的动作，由 BFF 校验后落账。
	ActVerified = "i3-mark-verified"
)

// 事件名。
const (
	EventPublish = "publish"
)

// Pub/Sub 主题。主题名是 BFF 与订阅方之间的契约，集中定义。
const (
	TopicTagCreated  = "tag.created"
	TopicItemChanged = "item.changed"
)

// 发布闸的等待上限。原型说「等人等三天」，这里给 30 天：
// 超时不是失败，而是把决定权交回给人（记为 blocked，可重启新实例）。
const publishWait = 30 * 24 * time.Hour

// 每个 activity 的重试：网络抖动重来一次，业务拒绝不重试
// （业务拒绝走 StepResult.OK=false，不是 error）。
var retry = &RetryPolicy{
	MaxAttempts:          3,
	InitialRetryInterval: time.Second,
	BackoffCoefficient:   2,
	MaxRetryInterval:     10 * time.Second,
}

// Affected 是变更类型影响到的制品（原型 TYPE_META.aff 的服务端版本）。
//
// 代码制品（code）也在这里：它才是构建与部署的对象——不改代码的「纯文档」
// 变更（doc）不碰它。backlog 只在新建与纯登记类变更里动。
func Affected(t string) []domain.Artifact {
	switch t {
	case "new":
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT, domain.Backlog, domain.Code}
	case "feature":
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT, domain.Code}
	case "defect":
		return []domain.Artifact{domain.AT, domain.AD, domain.Code}
	case "backlog":
		return []domain.Artifact{domain.Backlog}
	case "doc":
		return []domain.Artifact{domain.PRD}
	default:
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT, domain.Code}
	}
}

// Complete 补齐缺失的制品：提交要求整棵树都在（引用闭合闸）。
//
// 新建的交付单元第一次交付时，账本里一个制品都没有，只按变更类型生成
// 会凑不齐一棵树，提交就会被「引用不闭合」拒掉——那是正确的拒绝，
// 但流程不该走到那一步才失败。这里提前把缺的补上。
func Complete(affected []domain.Artifact, have map[domain.Artifact]bool) []domain.Artifact {
	out := append([]domain.Artifact(nil), affected...)
	seen := map[domain.Artifact]bool{}
	for _, t := range out {
		seen[t] = true
	}
	for _, t := range domain.Order {
		if !seen[t] && !have[t] {
			out = append(out, t)
		}
	}
	return out
}
