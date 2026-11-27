// Package codegen 是 X worker 的产物生成器：把一条工作项变成真实的制品内容。
//
// 为什么要有它：以前的「生成制品」只落一条带 note 的版本，内容是空的。
// 于是下游的测试、构建、部署都只能对着空壳编数字——那不是执行侧，
// 是脚手架。这里的生成器产出真的东西：
//
//   - 文档类（PRD / AD / AT / Backlog）：markdown 正文，可读可编辑。
//   - 代码类（code）：一个能 `go build`、能 `go test` 的 Go 模块，
//     没有对应领域实现时只生成标明范围的脚手架。
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
	UnitID      string   // 交付单元 id
	UnitName    string   // 交付单元名
	Kind        string   // pbc | agent | hybrid
	Profile     string   // 业务 PBC 的制品类型，空表示无领域模板
	DemoBilling bool     // 保留显式演示数据的缴费模板
	Accept      []string // 账本中由业务人员填写的工作项口径
	Title       string   // 工作项标题
	Change      string   // new | feature | defect | backlog | doc
	At          string   // 时间戳
}

// Plural 是资源集合名（URL 路径用）。
func (s Spec) Plural() string {
	switch s.UnitID {
	case "fee":
		return "bills"
	case "qa":
		return "reviews"
	case "dun":
		return "collections"
	default:
		if s.UnitID == "" {
			return "records"
		}
		return s.UnitID + "s"
	}
}

// Acceptance 是一条验收标准。
type Acceptance struct {
	ID    string
	Name  string
	Given string
	When  string
	Then  string
}

// Acceptance 返回已知领域的标准，或从工作项口径整理的待确认草案。
func (s Spec) Acceptance() []Acceptance {
	if s.Profile == domain.ProfileMaterialInbound {
		return materialAcceptance
	}
	if !s.DemoBilling {
		criteria, _ := s.scaffoldCriteria()
		return criteria
	}
	return []Acceptance{
		{ID: "AC-1", Name: "健康检查可用", Given: "服务已启动", When: "请求 /healthz", Then: "返回 200 且带单元标识"},
		{ID: "AC-2", Name: "列表可读", Given: "单元内有若干条记录", When: "请求列表接口", Then: "返回非空列表，且金额与逾期天数可读"},
		{ID: "AC-3", Name: "批量生成幂等", Given: "带同一个幂等键提交两次", When: "调用生成接口", Then: "只入账一次，第二次返回同一结果"},
		{ID: "AC-4", Name: "滞纳金封顶", Given: "逾期 400 天、金额 10000", When: "计算滞纳金", Then: "封顶在 2000（本金 20%），逾期更久也不增加"},
		{ID: "AC-5", Name: "统计口径一致", Given: "列表非空", When: "请求统计接口", Then: "条数与金额合计与列表一致"},
	}
}

// render 做占位符替换。{{KEY}} 会被替换成对应值。
func render(tmpl string, subs map[string]string) string {
	pairs := make([]string, 0, len(subs)*2)
	for k, v := range subs {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// Docs 生成文档类制品的 markdown 正文。
func Docs(s Spec) map[domain.Artifact]string {
	if !s.DemoBilling && s.Profile != domain.ProfileMaterialInbound {
		return s.scaffoldDocs()
	}
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
	if s.Profile == domain.ProfileMaterialInbound {
		return "# 材料入库原型\n\n入库单按类型带出来源单据、资产归属与财务口径，提交前核对计划余量。库房收发存明细支持按入库单号追溯。可操作界面随当前构建的业务制品发布。"
	}
	return render(protoTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At,
	})
}

func (s Spec) prd() string {
	if s.Profile == domain.ProfileMaterialInbound {
		return "# 材料入库\n\n支持采购入库和甲供入库。确认前核对来源与计划余量，确认后按单号追溯材料、数量及归属。"
	}
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
	if s.Profile == domain.ProfileMaterialInbound {
		return "# 材料入库技术方案\n\nPOST /api/inbounds 校验来源、库房、单位及余量。确认事实固化入库明细与扣减后余量，GET /api/inbounds/{单号} 回放追溯。数量按千分位整数存算。"
	}
	return render(adTmpl, map[string]string{
		"UNIT": s.UnitName, "TITLE": s.Title, "AT": s.At, "PLURAL": s.Plural(),
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

// Code 生成可编译、可测试的 Go 模块。
func Code(s Spec) map[string]string {
	if s.Profile == domain.ProfileMaterialInbound {
		return materialCode(s)
	}
	if !s.DemoBilling {
		return s.scaffoldCode()
	}
	p := s.Plural()
	mod := "i3/" + s.UnitID
	if s.UnitID == "" {
		mod = "i3/unit"
	}
	subs := map[string]string{
		"UNIT":   s.UnitName,
		"TITLE":  s.Title,
		"AT":     s.At,
		"UNITID": s.UnitID,
		"PLURAL": p,
		"MOD":    mod,
	}
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
