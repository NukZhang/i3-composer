// Package codegen 是 X worker 的产物生成器：把一条工作项变成真实的制品内容。
//
// 为什么要有它：以前的「生成制品」只落一条带 note 的版本，内容是空的。
// 于是下游的测试、构建、部署都只能对着空壳编数字——那不是执行侧，
// 是脚手架。这里的生成器产出真的东西：
//
//   - 文档类（PRD / AD / AT / Backlog）：markdown 正文，可读可编辑。
//   - 代码类（code）：一个能 `go build`、能 `go test` 的 Go 模块，
//     它的行为由验收标准（AT）决定，所以测试跑的是真实现。
//
// 生成是确定性的：同样的输入永远得到同样的字节，所以 digest 可复算。
//
// 模板用占位符替换而不是 fmt.Sprintf：生成的代码里有 % 和 {}，
// 交给 Sprintf 会被当成格式符吃掉。
package codegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// Spec 是一次生成所需的全部输入。
type Spec struct {
	UnitID   string // 交付单元 id
	UnitName string // 交付单元名
	Kind     string // pbc | agent | hybrid
	Title    string // 工作项标题
	Change   string // new | feature | defect | backlog | doc
	At       string // 时间戳
}

// Acceptance 是一条验收标准。
type Acceptance struct {
	ID    string
	Name  string
	Given string
	When  string
	Then  string
}

// Acceptance 是本单元的验收标准清单。
//
// 文档（AT）与代码（单元测试）共用同一份清单：文档里写「Then 什么」，
// 测试里就断言什么。这样「文档说一套、代码做另一套」不会发生。
//
// 这里只给「这套骨架本来就能被验的」五条——健康检查、列表、幂等、统计一致、
// 错误可读。
// 业务口径（费率、上限、状态流转）必须来自 spec 制品，不能由模板替客户拍板（#93）。
func (s Spec) Acceptance() []Acceptance {
	w := s.Terms().Word
	return []Acceptance{
		{ID: "AC-1", Name: "健康检查可用", Given: "服务已启动", When: "请求 /healthz", Then: "返回 200 且带单元标识"},
		{ID: "AC-2", Name: w + "列表可读", Given: "单元内有若干条" + w, When: "请求列表接口", Then: "返回非空列表，每条都有 id 与业务字段"},
		{ID: "AC-3", Name: "批量生成幂等", Given: "带同一个幂等键提交两次", When: "调用生成接口", Then: "只入账一次，第二次返回同一结果"},
		{ID: "AC-4", Name: "统计口径一致", Given: "列表非空", When: "请求统计接口", Then: "条数与列表一致"},
		{ID: "AC-5", Name: "错误可读", Given: "请求体不是合法 JSON", When: "调用生成接口", Then: "返回 400 且带一句原因"},
	}
}

// render 做占位符替换。{{KEY}} 会被替换成对应值。
func render(tmpl string, subs map[string]string) string {
	out := tmpl
	for k, v := range subs {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// Docs 生成文档类制品的 markdown 正文。
func Docs(s Spec) map[domain.Artifact]string {
	out := map[domain.Artifact]string{}
	out[domain.PRD] = s.prd()
	out[domain.Proto] = s.proto()
	out[domain.AD] = s.ad()
	out[domain.AT] = s.at()
	out[domain.Backlog] = s.backlog()
	return out
}

// proto 是原型说明：这块交付物长什么样、用户怎么用它。
func (s Spec) proto() string {
	return render(protoTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At,
	})
}

func (s Spec) prd() string {
	goal := map[string]string{
		"new":     "新增一个此前不存在的交付能力",
		"feature": "在已有能力上扩展边界",
		"defect":  "修掉一个已经暴露的问题",
		"backlog": "把待办登记进需求池",
		"doc":     "只补文档，不动行为",
	}[s.Change]
	if goal == "" {
		goal = "交付一件可验收的东西"
	}
	return render(prdTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At, "GOAL": goal,
	})
}

func (s Spec) ad() string {
	return render(adTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At, "PLURAL": s.Terms().Plural,
	})
}

func (s Spec) at() string {
	return render(atTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At, "AC": s.acceptance(),
	})
}

func (s Spec) acceptance() string {
	var b strings.Builder
	for _, a := range s.Acceptance() {
		fmt.Fprintf(&b, "### %s %s\n- Given：%s\n- When：%s\n- Then：%s\n\n", a.ID, a.Name, a.Given, a.When, a.Then)
	}
	return b.String()
}

func (s Spec) backlog() string {
	var b strings.Builder
	b.WriteString(render(backlogTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At, "ID": s.UnitID,
	}))
	for _, a := range s.Acceptance() {
		fmt.Fprintf(&b, "- [ ] %s 实现并自测：%s\n", a.ID, a.Then)
	}
	return b.String()
}

// Code 生成代码类制品：一个能编译、能测试的 Go 模块。
//
// 关键点：它实现的是 AT 里与业务无关的那几条（健康、列表、幂等、统计一致），
// 所以 `go test` 跑的是真实现，而不是对着空壳编出来的条数。
func Code(s Spec) map[string]string {
	subs := codeSubs(s)
	subs["UNITID"] = s.UnitID
	return map[string]string{
		"go.mod":       render(goModTmpl, subs),
		"main.go":      render(mainTmpl, subs),
		"main_test.go": render(mainTestTmpl, subs),
	}
}

// Files 把代码制品落成「路径 → 内容」的形式，顺序稳定：
// map 遍历顺序不定，而 digest 必须与顺序无关。
func Files(s Spec) []File {
	m := Code(s)
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		out = append(out, File{Path: p, Content: m[p]})
	}
	return out
}

// File 是一个文件。
type File struct {
	Path    string
	Content string
}

// JSON 是给 Agent 定义用的稳定 JSON 编码。
func JSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
