// Package domain 是 L3 的「判断」层：纯函数，不读时钟、不读环境、不碰 IO。
//
// 对应原型 i3-composer v2.1 的内核约束：
//   - 事实日志唯一权威  → 本包不产生事实，只对给定快照给出判断
//   - 判断是纯函数      → 入参即全部输入，同样输入必然同样输出
//   - 投影可重建        → 所有判断都基于 (版本线, 提交线)，两者都能从日志重放
package domain

import "fmt"

// Artifact 是制品类型。顺序即依赖顺序，不能在别处另行定义一份。
type Artifact string

const (
	PRD     Artifact = "prd"
	Proto   Artifact = "proto"
	AD      Artifact = "ad"
	AT      Artifact = "at"
	Backlog Artifact = "backlog"
	Code    Artifact = "code"
)

// Order 是制品的固定解析顺序（PRD → 原型 → AD → AT → Backlog → 代码）。
var Order = []Artifact{PRD, Proto, AD, AT, Backlog, Code}

// PlanArtifacts 是「方案」页展示的制品（原型单独一页，代码在构建页）。
var PlanArtifacts = []Artifact{PRD, AD, AT, Backlog}

func (a Artifact) Valid() bool {
	for _, x := range Order {
		if x == a {
			return true
		}
	}
	return false
}

func (a Artifact) String() string { return string(a) }

// Version 状态。superseded 表示被同制品更新版本取代的草稿。
const (
	StDraft      = "draft"
	StAccepted   = "accepted"
	StSuperseded = "superseded"
)

// Ref 是一条「衍生自」引用：本制品这一版是基于上游哪个版本生成的。
type Ref struct {
	T Artifact `json:"t"`
	V string   `json:"v"`
}

// Version 是一个制品版本。V 形如 1.0.3。
// At 由调用方传入（本包不读时钟），保证纯函数。
type Version struct {
	T      Artifact `json:"t"`
	V      string   `json:"v"`
	At     string   `json:"at"`
	By     string   `json:"by"`
	Source string   `json:"source"` // 手动 | 生成 | 重算
	DF     []Ref    `json:"df"`
	St     string   `json:"st"`
	Note   string   `json:"note"`
	// Digest 仅代码制品在构建后被回写，用于「镜像与树一致」闸。
	Digest string `json:"digest,omitempty"`
	// MD / Files 是制品内容：文档类存 md，代码类存文件树。
	// 它们随版本一起进日志，所以「保存草稿」是一件真实的事，不是前端内存。
	MD    string            `json:"md,omitempty"`
	Files map[string]string `json:"files,omitempty"`
}

// Tree 是一棵制品树的快照：制品 → 版本号。
type Tree map[Artifact]string

// Latest 从版本线里取最新版本号；没有则返回空串。
func Latest(versions map[Artifact][]Version, t Artifact) string {
	vs := versions[t]
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1].V
}

// Find 在版本线里按版本号查找。
func Find(versions map[Artifact][]Version, t Artifact, v string) (Version, bool) {
	for _, x := range versions[t] {
		if x.V == v {
			return x, true
		}
	}
	return Version{}, false
}

// DeriveRefs 是原型 dfRule 的纯函数版本，也是「引用闭合」闸的判定依据。
//
// 每一步都只允许引用上游的「最新」版本：这样 stale 检测才是可判定的。
func DeriveRefs(t Artifact, latest func(Artifact) string) []Ref {
	ref := func(x Artifact) Ref { return Ref{T: x, V: latest(x)} }
	switch t {
	case PRD:
		return nil
	case Proto:
		return []Ref{ref(PRD)}
	case AD:
		return []Ref{ref(PRD), ref(Proto)}
	case AT:
		return []Ref{ref(PRD), ref(AD)}
	case Code:
		return []Ref{ref(AT), ref(Backlog)}
	default: // backlog
		return []Ref{ref(AT)}
	}
}

// StaleItem 表示某制品的最新版本引用了非最新的上游。
type StaleItem struct {
	T  Artifact `json:"t"`
	V  string   `json:"v"`
	On []Ref    `json:"on"`
}

// Stale 列出所有过期制品。按 Order 顺序返回，调用方可直接依次重算。
func Stale(versions map[Artifact][]Version) []StaleItem {
	out := make([]StaleItem, 0, len(Order))
	for _, t := range Order {
		v := Latest(versions, t)
		if v == "" {
			continue
		}
		cur, ok := Find(versions, t, v)
		if !ok {
			continue
		}
		var bad []Ref
		for _, d := range cur.DF {
			if up := Latest(versions, d.T); up != "" && up != d.V {
				bad = append(bad, d)
			}
		}
		if len(bad) > 0 {
			out = append(out, StaleItem{T: t, V: v, On: bad})
		}
	}
	return out
}

// Drafts 返回各制品当前的草稿（每个制品最多一条）。
func Drafts(versions map[Artifact][]Version) []Version {
	out := make([]Version, 0, len(Order))
	for _, t := range Order {
		v := Latest(versions, t)
		if v == "" {
			continue
		}
		cur, ok := Find(versions, t, v)
		if ok && cur.St == StDraft {
			out = append(out, cur)
		}
	}
	return out
}

// NextVersion 在给定版本号上加一个补丁位：1.0.2 → 1.0.3。
func NextVersion(cur string) (string, error) {
	if cur == "" {
		return "1.0.1", nil
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(cur, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return "", fmt.Errorf("版本号格式非法 %q：只接受 major.minor.patch", cur)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch+1), nil
}
