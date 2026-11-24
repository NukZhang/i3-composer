package domain

import "fmt"

// 知识库是 Agent 的外部记忆，不是它自己学会的。
//
// 两处接入（原型 v2.4 / v2.6）：
//   - 构建：依赖清单（几个库、版本、同步方式、topK/阈值/重排）+ 工具接线检查
//   - 验证：准确度（Recall@5 / MRR / 忠实度 / 引用命中）+ 失败归因 + 问题单
//
// 一条硬规矩：**库版本进 digest**。库换了、digest 没换，是最容易漏的
// 发布事故——评测还停在旧库上，线上已经用新库回答。

// KBLib 是一个被引用的知识库。
type KBLib struct {
	N     string `json:"n"`
	Docs  int    `json:"docs"`
	V     string `json:"v"`
	Sync  string `json:"sync"` // T+1 | 手动
	Owner string `json:"owner"`
}

// KB 是一名 Agent 的知识库配置与准确度。
type KB struct {
	Libs   []KBLib   `json:"libs"`
	TopK   int       `json:"topK"`
	Thr    float64   `json:"thr"`
	Rerank bool      `json:"rerank"`
	Recall float64   `json:"recall"`   // Recall@5
	MRR    float64   `json:"mrr"`      // 首条命中
	Faith  float64   `json:"faithful"` // 忠实度
	Cite   float64   `json:"cite"`     // 引用命中率
	Attr   KBAttr    `json:"attr"`     // 失败归因
	Issues []KBIssue `json:"issues"`
}

// KBAttr 把失败拆成「没召回」和「答歪」，因为两件事该谁改完全不同。
type KBAttr struct {
	NotFound int `json:"notFound"`
	WrongGen int `json:"wrongGen"`
}

// KBIssue 是知识库问题单：缺文档 / 过期 / 两份文档打架。
type KBIssue struct {
	N          string `json:"n"`
	C          int    `json:"c"`
	Ex         string `json:"ex"`
	Dispatched bool   `json:"dispatched,omitempty"` // 已派给库的 owner
}

// Ref 拼出「库名@版本」的指纹片段，供 digest 使用。
func (k *KB) Ref() string {
	if k == nil || len(k.Libs) == 0 {
		return ""
	}
	out := ""
	for _, l := range k.Libs {
		out += fmt.Sprintf("%s@%s;", l.N, l.V)
	}
	return out
}

// KBIssueTotal 是问题单里待修的条数。
func (k *KB) KBIssueTotal() int {
	if k == nil {
		return 0
	}
	n := 0
	for _, x := range k.Issues {
		n += x.C
	}
	return n
}

// KBFailTotal 是归因合计；它应当等于评测失败数，否则口径不一致。
func (k *KB) KBFailTotal() int {
	if k == nil {
		return 0
	}
	return k.Attr.NotFound + k.Attr.WrongGen
}
