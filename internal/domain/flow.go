package domain

import (
	"fmt"
	"strings"
)

// 本文件是原型 v2.6 的「Agent 编排」领域模型。
//
// 一个 Agent 不是一个配置文件，而是一张流程图：消息进来先查哪个库、
// 查不到怎么办、什么时候调工具、什么时候找人确认。七个节点类型各自
// 有不同的配置项，digest 由整张图（节点 + 连线 + 配置）决定。

// 节点类型。与原型 AG_T 一一对应。
const (
	NodeStart  = "start"
	NodeTool   = "tool"
	NodeKB     = "kb"
	NodeLLM    = "llm"
	NodeBranch = "branch"
	NodeHITL   = "hitl"
	NodeEnd    = "end"
)

// FlowNode 是流程图上的一个节点。
//
// Cfg 用 map 承载：七种节点的配置项不同（变量表 / 工具入参 / 库参数 /
// 提示词 / 分支条件 / 等谁 / 输出字段）。用一个宽接口，比七套结构体更
// 贴近「原型里同一张画布」这件事。
type FlowNode struct {
	ID  string         `json:"id"`
	N   string         `json:"n"`
	T   string         `json:"t"`
	C   int            `json:"c"` // 画布列
	R   int            `json:"r"` // 画布行
	Sub string         `json:"sub,omitempty"`
	Ms  int            `json:"ms"`
	Tok int            `json:"tok"`
	Cfg map[string]any `json:"cfg,omitempty"`
}

// Flow 是一张编排。Edges 用 [from,to,label] 三元组，label 是分支条件。
type Flow struct {
	Nodes []FlowNode  `json:"nodes"`
	Edges [][3]string `json:"edges"`
}

// Valid 判定编排是否可构建：有节点、类型合法、id 唯一、恰好一个开始、至少一个结束、
// 每条边都指向存在的节点。缺一条都不能算「可执行的规格」。
func (f *Flow) Valid() error {
	if f == nil || len(f.Nodes) == 0 {
		return fmt.Errorf("编排为空：至少要有开始、模型与结束节点")
	}
	ids := map[string]*FlowNode{}
	starts, ends := 0, 0
	for i := range f.Nodes {
		n := &f.Nodes[i]
		if n.ID == "" {
			return fmt.Errorf("第 %d 个节点缺少 id", i+1)
		}
		if _, dup := ids[n.ID]; dup {
			return fmt.Errorf("节点 %s 重复", n.ID)
		}
		switch n.T {
		case NodeStart:
			starts++
		case NodeEnd:
			ends++
		case NodeTool, NodeKB, NodeLLM, NodeBranch, NodeHITL:
		default:
			return fmt.Errorf("节点 %s 的类型 %q 不合法", n.ID, n.T)
		}
		ids[n.ID] = n
	}
	if starts != 1 {
		return fmt.Errorf("编排必须恰好一个开始节点（现在是 %d 个）", starts)
	}
	if ends == 0 {
		return fmt.Errorf("编排至少要有一个结束节点")
	}
	for _, e := range f.Edges {
		if ids[e[0]] == nil {
			return fmt.Errorf("连线指向不存在的节点 %q", e[0])
		}
		if ids[e[1]] == nil {
			return fmt.Errorf("连线指向不存在的节点 %q", e[1])
		}
	}
	return nil
}

// ToolUse 是一个工具在编排里的接线情况。
//
// 「白名单给了，流程里没接线」是一个有用的信号：要么流程漏了一步，
// 要么这个权限该收回去。所以工具表要多一列「用在哪」。
type ToolUse struct {
	Tool  string   `json:"tool"`
	Nodes []string `json:"nodes"`
	Wired bool     `json:"wired"`
}

// ToolWiring 从节点配置里反查每个白名单工具用在哪。
func ToolWiring(a *Agent) []ToolUse {
	if a == nil {
		return nil
	}
	out := make([]ToolUse, 0, len(a.Tools))
	for _, t := range a.Tools {
		u := ToolUse{Tool: t}
		if a.Flow != nil {
			for _, n := range a.Flow.Nodes {
				if nodeUsesTool(n, t) {
					u.Nodes = append(u.Nodes, n.N)
				}
			}
		}
		u.Wired = len(u.Nodes) > 0
		out = append(out, u)
	}
	return out
}

func nodeUsesTool(n FlowNode, tool string) bool {
	if n.Cfg == nil {
		return false
	}
	if s, _ := n.Cfg["tool"].(string); s == tool {
		return true
	}
	if arr, ok := n.Cfg["tools"].([]any); ok {
		for _, v := range arr {
			if s, _ := v.(string); s == tool {
				return true
			}
		}
	}
	if pre, _ := n.Cfg["pre"].(string); pre != "" && strings.Contains(pre, tool) {
		return true
	}
	return false
}

// Trial 是「试跑这一条」的输入与输出样例。
//
// 轨迹按节点耗时回填，所以它和 Flow 是同一份数据的两面：
// 看钱花在哪个节点，改提示词才知道该动哪一段。
type Trial struct {
	In  string   `json:"in"`
	Out []string `json:"out,omitempty"`
	Raw string   `json:"raw,omitempty"`
}

// Chain 返回这一轮的逐节点耗时链（只取真正耗时的节点）。
func (f *Flow) Chain() []string {
	if f == nil {
		return nil
	}
	var out []string
	for _, n := range f.Nodes {
		if n.Ms <= 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s", n.N, duration(n.Ms)))
	}
	return out
}

func duration(ms int) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}
