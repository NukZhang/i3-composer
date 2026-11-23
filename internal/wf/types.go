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

// Input 是一个工作项进入 workflow 时的全部输入。
type Input struct {
	ItemID string `json:"itemId"`
	PBC    string `json:"pbc"`
	Title  string `json:"title"`
	Type   string `json:"type"` // new | feature | defect | backlog | doc
	Env    string `json:"env"`
	Mode   string `json:"mode"` // fast | guided
}

// StepResult 是单个 activity 的结果。只回报「成没成」+ 一句说明；
// 真正的状态在事实日志里，不靠 workflow history 传递。
type StepResult struct {
	OK     bool   `json:"ok"`
	Step   int    `json:"step"`
	Detail string `json:"detail"`
	// Pass/Total 仅测试步使用。
	Pass  int `json:"pass,omitempty"`
	Total int `json:"total,omitempty"`
	// CommitNo / Tag 由提交、打标两步回传。
	CommitNo string `json:"commitNo,omitempty"`
	Tag      string `json:"tag,omitempty"`
	URL      string `json:"url,omitempty"`
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
func Affected(t string) []domain.Artifact {
	switch t {
	case "new":
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT, domain.Backlog}
	case "feature":
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT}
	case "defect":
		return []domain.Artifact{domain.AT, domain.AD}
	case "backlog":
		return []domain.Artifact{domain.Backlog}
	case "doc":
		return []domain.Artifact{domain.PRD}
	default:
		return []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT}
	}
}
