package bff

import (
	"net/http"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 一张最小可执行编排：开始 → 工具 → 结束。
func demoFlow(tool string) *domain.Flow {
	return &domain.Flow{
		Nodes: []domain.FlowNode{
			{ID: "n1", N: "任务进入", T: domain.NodeStart},
			{ID: "n2", N: "干活", T: domain.NodeTool, Cfg: map[string]any{"tool": tool, "approve": "不需要（只读）"}},
			{ID: "n3", N: "回执", T: domain.NodeEnd},
		},
		Edges: [][3]string{{"n1", "n2", ""}, {"n2", "n3", ""}},
	}
}

func TestAgentFlowSaveChangesDigestUntilBuild(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.Flow = demoFlow("repo.read")
	seedAgent(t, h.st, a, 48, 50)

	// 改编排 → digest 变，但没构建：闸门必须红。
	f2 := demoFlow("repo.write")
	resp, out := h.do(t, http.MethodPut, "/api/agents/x-fe/flow", map[string]any{"flow": f2}, RoleDA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存编排应 200，实际 %d %v", resp.StatusCode, out)
	}
	if out["dirty"] != true {
		t.Fatalf("改了编排但没构建，dirty 应为 true：%v", out)
	}
	_, st := h.do(t, http.MethodGet, "/api/agents/x-fe", nil, RoleBA)
	for _, g := range st["gates"].([]any) {
		m := g.(map[string]any)
		if m["key"] == "agent-digest" && m["ok"] == true {
			t.Fatal("编排改了没构建，digest 闸不应通过")
		}
	}

	// 没构建就评测：拒绝。
	resp, _ = h.do(t, http.MethodPost, "/api/agents/x-fe/eval",
		map[string]any{"pass": 48, "total": 50}, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未构建就评测应 400，实际 %d", resp.StatusCode)
	}

	// 构建 → 版本号前进、闸门转绿。
	resp, out = h.do(t, http.MethodPost, "/api/agents/x-fe/build", nil, RoleDA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("构建应 201，实际 %d %v", resp.StatusCode, out)
	}
	if out["ver"] == a.Ver {
		t.Fatalf("构建应换版本号，实际 %v", out["ver"])
	}
	_, st = h.do(t, http.MethodGet, "/api/agents/x-fe", nil, RoleBA)
	for _, g := range st["gates"].([]any) {
		m := g.(map[string]any)
		if m["key"] == "agent-digest" && m["ok"] != true {
			t.Fatalf("构建后 digest 闸应绿：%v", m)
		}
	}
}

func TestAgentBuildWithoutChangesIsNotNewVersion(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.Flow = demoFlow("repo.read")
	seedAgent(t, h.st, a, 48, 50)
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-fe/build", nil, RoleDA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("没有改动时构建应 200（不制造空版本），实际 %d %v", resp.StatusCode, out)
	}
	if out["changed"] != false {
		t.Fatalf("没有改动不该换版本：%v", out)
	}
}

func TestAgentFlowSaveRejectsBrokenFlow(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.Flow = demoFlow("repo.read")
	seedAgent(t, h.st, a, 48, 50)
	// 没有开始节点。
	bad := &domain.Flow{Nodes: []domain.FlowNode{{ID: "n1", N: "只有结束", T: domain.NodeEnd}}}
	resp, _ := h.do(t, http.MethodPut, "/api/agents/x-fe/flow", map[string]any{"flow": bad}, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("不合法编排应 400，实际 %d", resp.StatusCode)
	}
}

func TestAcceptanceDecideCreatesDefectAndBacklogItem(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	registerUnit(t, h.st, "fee") // #43：/api/state 只认登记过的单元
	if _, err := h.st.Append(ctx, "fee", store.FactCheckDefined, "BA", map[string]any{
		"items": []domain.Check{
			{ID: "AC-1", N: "账期按自然月生成", G: "14 个项目", W: "点生成", T: "生成 14 份"},
			{ID: "AC-2", N: "批量生成缴费单", G: "勾选 32 户", W: "点批量生成", T: "二次确认后生成"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// 通过：只改判定，不提 bug。
	resp, out := h.do(t, http.MethodPost, "/api/checks/AC-1/decide",
		map[string]any{"st": "pass"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("判定应 201，实际 %d %v", resp.StatusCode, out)
	}

	// 不通过：必须写 bug 标题；bug 进 backlog。
	resp, _ = h.do(t, http.MethodPost, "/api/checks/AC-2/decide",
		map[string]any{"st": "fail"}, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("不提 bug 标题应 400，实际 %d", resp.StatusCode)
	}
	resp, out = h.do(t, http.MethodPost, "/api/checks/AC-2/decide",
		map[string]any{"st": "fail", "title": "筛选后按钮消失", "sev": "严重",
			"steps": "打开列表 → 勾选 A 栋 32 户 → 点批量生成"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提 bug 应 201，实际 %d %v", resp.StatusCode, out)
	}
	defect, _ := out["defect"].(map[string]any)
	if defect == nil || defect["id"] != "BUG-118" {
		t.Fatalf("bug 应有编号：%v", out)
	}

	_, st := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	checks := st["checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("清单应有两条：%v", checks)
	}
	byID := map[string]map[string]any{}
	for _, c := range checks {
		m := c.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["AC-1"]["st"] != "pass" || byID["AC-2"]["st"] != "fail" {
		t.Fatalf("判定没进投影：%v", byID)
	}
	defects := st["defects"].([]any)
	if len(defects) != 1 || defects[0].(map[string]any)["ac"] != "AC-2" {
		t.Fatalf("bug 应进投影：%v", defects)
	}
	// bug 变成 backlog 里的一个修复项。
	items := st["items"].(map[string]any)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["type"] == "defect" {
			found = true
			accept, _ := m["accept"].([]any)
			if len(accept) != 1 || accept[0] != "Given 勾选 32 户，When 点批量生成，Then 二次确认后生成" {
				t.Errorf("修复项应继承原验收口径：%v", m)
			}
			repair, _ := m["repair"].(map[string]any)
			if repair["check"] != "AC-2" || repair["defect"] != defect["id"] {
				t.Errorf("修复项应关联原缺陷和验收项：%v", repair)
			}
		}
	}
	if !found {
		t.Fatalf("bug 应进 backlog：%v", items)
	}

	// 越权：DA 不能替 BA 验收。
	resp, _ = h.do(t, http.MethodPost, "/api/checks/AC-1/decide",
		map[string]any{"st": "pass"}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 验收应 403，实际 %d", resp.StatusCode)
	}
}

func TestRepairAdmitsWithInputsFromCurrentCommit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	seedTagCandidate(t, h, "fee")
	check := domain.Check{ID: "AT-01", G: "已有账单", W: "查询列表", T: "显示本期账单", ST: domain.CheckTodo}
	seedNewCommit(t, h.st, "fee", "1.0.2", check)
	seedItem(t, h.st, store.Item{
		ID: "US-01", PBC: "fee", Kind: "Story", Title: "收费列表",
		Data: []string{"fee.bill.amount"}, AcceptBy: "收费验收岗", ComplyBy: "合规岗",
	})
	if _, err := h.st.Append(ctx, "fee", store.FactTestRun, "x-qa", store.TestResult{
		ItemID: "US-01", Commit: "1.0.2", Pass: 5, Total: 5,
	}); err != nil {
		t.Fatal(err)
	}
	resp, body := h.do(t, http.MethodPost, "/api/checks/AT-01/decide?pbc=fee",
		map[string]string{"st": "fail", "commit": "1.0.2", "title": "列表缺少本期账单"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提缺陷返回 %d：%v", resp.StatusCode, body)
	}
	defect := body["defect"].(map[string]any)
	id, _ := defect["repairItem"].(string)
	if id != "US-02" {
		t.Fatalf("应关联新修复项：%v", defect)
	}
	resp, detail := h.do(t, http.MethodGet, "/api/items/"+id+"?pbc=fee", nil, RoleBA)
	inputs, _ := detail["repairInputs"].(map[string]any)
	if resp.StatusCode != http.StatusOK || inputs["reasons"] != nil ||
		inputs["acceptBy"] != "收费验收岗" || inputs["complyBy"] != "合规岗" {
		t.Fatalf("应继承当前提交相关工作项的准入输入：%d %v", resp.StatusCode, detail)
	}
	resp, result := h.do(t, http.MethodPost, "/api/items/"+id+"/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("DA 应能直接发起修复：%d %v", resp.StatusCode, result)
	}
	before, err := h.st.Load(ctx, "fee")
	if err != nil {
		t.Fatal(err)
	}
	resp, result = h.do(t, http.MethodPost, "/api/checks/AT-01/decide?pbc=fee",
		map[string]string{"st": "pass", "commit": "1.0.2"}, RoleBA)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("运行中的修复不能提前判过：%d %v", resp.StatusCode, result)
	}
	after, err := h.st.Load(ctx, "fee")
	if err != nil || len(after) != len(before) {
		t.Fatalf("提前判过被拒时不得写账本：%d → %d，%v", len(before), len(after), err)
	}
}

func TestVerifyChatRecordsBothSides(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	seedAgent(t, h.st, goodAgent(), 48, 50)
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "qa", "name": "质检", "kind": "agent", "agentId": "x-fe",
	}); err != nil {
		t.Fatal(err)
	}
	resp, out := h.do(t, http.MethodPost, "/api/units/qa/chat",
		map[string]any{"text": "你们这个能维持多久？"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("验收对话应 201，实际 %d %v", resp.StatusCode, out)
	}
	if out["reply"] == "" {
		t.Fatal("应有回复")
	}
	_, st := h.do(t, http.MethodGet, "/api/state?pbc=qa", nil, RoleBA)
	chats := st["chats"].([]any)
	if len(chats) != 2 {
		t.Fatalf("一问一答应两条事实，实际 %d", len(chats))
	}
}

func TestKBDispatchMarksIssue(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.KB = &domain.KB{
		Libs:   []domain.KBLib{{N: "产品口径字典", Docs: 412, V: "2026.11.1", Sync: "T+1", Owner: "市场"}},
		Issues: []domain.KBIssue{{N: "缺维持期口径", C: 1, Ex: "字典里没有维持期区间"}},
	}
	seedAgent(t, h.st, a, 48, 50)
	resp, _ := h.do(t, http.MethodPost, "/api/agents/x-fe/kb/0/dispatch", nil, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("派单应 201，实际 %d", resp.StatusCode)
	}
	_, st := h.do(t, http.MethodGet, "/api/agents/x-fe", nil, RoleBA)
	kb := st["kb"].(map[string]any)
	issues := kb["issues"].([]any)
	if issues[0].(map[string]any)["dispatched"] != true {
		t.Fatalf("问题单应标记为已派：%v", issues)
	}
	// 重复派：拒绝。
	resp, _ = h.do(t, http.MethodPost, "/api/agents/x-fe/kb/0/dispatch", nil, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("重复派单应 400，实际 %d", resp.StatusCode)
	}
}

func TestAgentCaseAddsToEvalSet(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.Flow = demoFlow("repo.read")
	seedAgent(t, h.st, a, 48, 50)
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-fe/cases",
		map[string]any{"v": "试跑沉淀", "in": "a", "out": "b", "exp": "c"}, RoleDA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("沉淀用例应 201，实际 %d %v", resp.StatusCode, out)
	}
	_, st := h.do(t, http.MethodGet, "/api/agents/x-fe", nil, RoleBA)
	cases := st["cases"].([]any)
	if len(cases) != len(a.Cases)+1 {
		t.Fatalf("用例应进评测集：%v", cases)
	}
}
