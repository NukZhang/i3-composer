package codegen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zhangkun/i3-composer/internal/domain"
)

const scaffoldNotice = "模板占位，非本单元口径"

// ScaffoldHealthTest 区分执行过的脚手架健康检查和人工补充的业务测试。
const ScaffoldHealthTest = "TestScaffoldHealthz"

const scaffoldHealthScope = "i3ScaffoldHealthScope"

var scaffoldDeclaration = regexp.MustCompile(`(?m)^[ \t]*const[ \t]+` + scaffoldHealthScope +
	`[ \t]*=[ \t]*"scaffold"[ \t]*(?://[^\n]*)?\r?$`)

// IsScaffoldRuntime 读取由生成服务声明的脚手架范围。健康检查的小改动不应改变业务结论。
func IsScaffoldRuntime(main string) bool {
	return scaffoldDeclaration.MatchString(main)
}

func (s Spec) scaffoldCriteria() (criteria []Acceptance, unparsed []string) {
	inputs := s.Accept
	if len(inputs) == 0 {
		// 标题也可提供条件草案，但仍由 BA 确认。
		inputs = []string{s.Title}
	}
	for i, input := range inputs {
		line := strings.Join(strings.Fields(input), " ")
		if line == "" {
			continue
		}
		id := fmt.Sprintf("AC-%d", i+1)
		criterion := Acceptance{ID: id, Name: "工作项验收口径"}
		if checks := domain.ChecksFromAT(id + " " + line); len(checks) == 1 {
			criterion.Given, criterion.When, criterion.Then = checks[0].G, checks[0].W, checks[0].T
		} else if condition, outcome, ok := splitCondition(line); ok {
			criterion.Given = fmt.Sprintf("在「%s」单元处理本工作项", s.UnitName)
			criterion.When, criterion.Then = condition, outcome
		} else {
			unparsed = append(unparsed, line)
			continue
		}
		criteria = append(criteria, criterion)
	}
	return criteria, unparsed
}

func splitCondition(line string) (condition, outcome string, ok bool) {
	rest, ok := strings.CutPrefix(line, "当")
	if !ok {
		return "", "", false
	}
	condition, outcome, ok = strings.Cut(rest, "则")
	condition = strings.Trim(condition, " \t，,；;。")
	condition = strings.TrimSpace(strings.TrimSuffix(condition, "时"))
	outcome = strings.Trim(outcome, " \t，,；;。")
	return condition, outcome, ok && condition != "" && outcome != ""
}

func (s Spec) scaffoldHeader(kind string) string {
	return fmt.Sprintf("# %s · %s · %s\n\n> %s。业务实现和测试待补。\n\n",
		s.UnitName, s.Title, kind, scaffoldNotice)
}

func (s Spec) scaffoldDocs() map[domain.Artifact]string {
	criteria, unparsed := s.scaffoldCriteria()
	var at, backlog strings.Builder
	at.WriteString(s.scaffoldHeader("验收草案"))
	at.WriteString("以下按工作项的验收输入整理，供 BA 确认。只有人工 AT 会进入验收清单。\n\n")
	backlog.WriteString(s.scaffoldHeader("Backlog"))
	fmt.Fprintf(&backlog, "## Story\n\n- [ ] US-%s %s\n\n## Task\n\n", s.UnitID, s.Title)
	for _, a := range criteria {
		fmt.Fprintf(&at, "### %s %s\n- Given：%s\n- When：%s\n- Then：%s\n\n", a.ID, a.Name, a.Given, a.When, a.Then)
		fmt.Fprintf(&backlog, "- [ ] %s 实现并补业务测试：%s；%s\n", a.ID, a.When, a.Then)
	}
	if len(unparsed) > 0 {
		at.WriteString("## 待补齐 Given、When、Then 的原始口径\n\n")
		for _, line := range unparsed {
			fmt.Fprintf(&at, "- %s\n", line)
		}
	}
	if len(criteria) == 0 {
		backlog.WriteString("- [ ] 与 BA 确认验收口径，补业务实现和测试。\n")
	}
	design := "数据模型和业务接口待实现。生成的服务只提供健康检查。"
	if s.Kind == "agent" {
		design = "生成制品为编排定义。业务节点和评测用例待补。"
	}
	return map[domain.Artifact]string{
		domain.PRD:     s.scaffoldHeader("需求草案") + s.scaffoldInputs() + "\n业务范围和验收标准请由 BA 确认。\n",
		domain.Proto:   s.scaffoldHeader("原型草案") + "用户步骤和页面待确认。\n",
		domain.AD:      s.scaffoldHeader("技术草案") + design + "\n",
		domain.AT:      at.String(),
		domain.Backlog: backlog.String(),
	}
}

func (s Spec) scaffoldInputs() string {
	if len(s.Accept) == 0 {
		return "工作项未单列验收输入，请查阅人工方案并确认业务口径。\n"
	}
	var b strings.Builder
	b.WriteString("## 工作项验收输入\n\n")
	for _, line := range s.Accept {
		fmt.Fprintf(&b, "- %s\n", strings.Join(strings.Fields(line), " "))
	}
	return b.String()
}

func (s Spec) scaffoldCode() map[string]string {
	id := s.UnitID
	if id == "" {
		id = "unit"
	}
	return map[string]string{
		"go.mod":       render(goModTmpl, map[string]string{"MOD": "i3/" + id}),
		"main.go":      render(scaffoldMain, map[string]string{"ID": strconv.Quote(s.UnitID), "NAME": strconv.Quote(s.UnitName)}),
		"main_test.go": scaffoldTest,
		"README.scaffold.md": s.scaffoldHeader("代码脚手架") + s.scaffoldInputs() +
			"\n`go test` 只检查 /healthz，无业务通过结论。实现业务后请同步更新服务的脚手架范围和说明，并补充对应测试。\n",
	}
}

const scaffoldMain = `// 模板占位，非本单元口径。服务只提供技术健康检查。
package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
)

const unitID = {{ID}}
const unitName = {{NAME}}
const ` + scaffoldHealthScope + ` = "scaffold"

` + scaffoldMux + `
func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listen=%s", ln.Addr().String())
	log.Fatal(http.Serve(ln, newMux()))
}
`

const scaffoldMux = `func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "unit": unitID, "name": unitName, "scope": ` + scaffoldHealthScope + `})
	})
	return mux
}
`

const scaffoldTest = `// 模板占位，非本单元口径。这里只测试基础健康检查。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ` + ScaffoldHealthTest + `(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查应返回 200，实际 %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["unit"] != unitID || body["scope"] != "scaffold" {
		t.Fatalf("健康检查应报告本单元及脚手架范围，实际 %v", body)
	}
}
`
