package bff

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// seedAgent 往 agents 日志里写一条定义 + 一次构建 + 一次评测，让四闸有据可判。
//
// v2.6 起「digest 一致」的含义是：当前定义算出的 digest 与最后一次构建
// 落下的 digest 相等；评测也必须跑在那个 digest 上。所以夹具先构建再评测。
func seedAgent(t *testing.T, st *store.Store, a domain.Agent, pass, total int) {
	t.Helper()
	ctx := t.Context()
	a.EnsureDigest()
	if _, err := st.Append(ctx, agentsLog, store.FactAgentDefined, "PA", a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, agentsLog, store.FactAgentBuilt, "DA", map[string]any{
		"agentId": a.ID, "ver": a.Ver, "digest": a.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, agentsLog, store.FactAgentEvalRun, "x-qa", map[string]any{
		"agentId": a.ID, "run": domain.EvalRun{ID: "exp-1", Ver: a.Ver, Pass: pass, Total: total, Digest: a.Digest},
	}); err != nil {
		t.Fatal(err)
	}
}

func goodAgent() domain.Agent {
	return domain.Agent{ID: "x-fe", Name: "X-全栈工程师", Ver: "1.4.2", Model: "deepseek-v3",
		Prompt: "把 AT 落成代码", Tools: []string{"repo.read", "repo.write"},
		Stages: []string{"done", "done", "done", "done", "todo"}, Eval: [2]int{46, 50}}
}

func TestUnitsEndpointReportsKindAndProgress(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	seedAgent(t, h.st, goodAgent(), 48, 50)
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "fee", "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "qa", "name": "交付质检", "kind": "agent", "agentId": "x-fe",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, "fee", store.FactUnitCreated, "PA", map[string]any{
		"name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	resp, out := h.do(t, http.MethodGet, "/api/units", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d %v", resp.StatusCode, out)
	}
	units := out["units"].([]any)
	if len(units) != 2 {
		t.Fatalf("应有两个单元，实际 %v", units)
	}
	qa := units[1].(map[string]any)
	if qa["kind"] != "agent" || qa["agent"] == nil {
		t.Fatalf("agent 单元必须带 Agent 视图：%v", qa)
	}
	fee := units[0].(map[string]any)
	if fee["items"].(float64) != 1 {
		t.Fatalf("单元进度应来自各 PBC 日志：%v", fee)
	}
}

func TestRemoveUnitPreservesFactsAndAllowsReregisteringID(t *testing.T) {
	h := newHarness(t)
	const path = "/api/units/retired?createdSeq=1"
	const statePath = "/api/state?pbc=retired"
	create := func(name string) {
		t.Helper()
		resp, out := h.do(t, http.MethodPost, "/api/units", map[string]string{
			"id": "retired", "name": name, "kind": "pbc",
		}, RolePA)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("登记应返回 201，实际 %d %v", resp.StatusCode, out)
		}
	}
	registry := func() []store.Fact {
		t.Helper()
		facts, err := h.st.Load(t.Context(), unitsLog)
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	create("旧登记")
	if _, err := h.st.Append(t.Context(), "retired", store.FactItemCreated, "BA", store.Item{
		ID: "US-01", Title: "历史工作项", Kind: "Story", PBC: "retired",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := h.st.Load(t.Context(), "retired")
	if err != nil || len(before) < 2 {
		t.Fatalf("单元历史未准备好：%+v，%v", before, err)
	}
	if resp, out := h.do(t, http.MethodDelete, path, nil, RoleDA); resp.StatusCode != http.StatusForbidden {
		t.Errorf("越权作废应返回 403，实际 %d %v", resp.StatusCode, out)
	}
	if facts := registry(); len(facts) != 1 {
		t.Fatalf("越权作废不得落账：%+v", facts)
	}
	if resp, out := h.do(t, http.MethodDelete, path, nil, RolePA); resp.StatusCode != http.StatusOK {
		t.Fatalf("PA 作废应返回 200，实际 %d %v", resp.StatusCode, out)
	}
	facts := registry()
	if len(facts) != 2 || facts[1].Type != store.FactUnitRemoved || facts[1].Actor != "PA" {
		t.Fatalf("应追加一条 PA 作废事实：%+v", facts)
	}
	if resp, _ := h.do(t, http.MethodDelete, path, nil, RolePA); resp.StatusCode != http.StatusNotFound || len(registry()) != 2 {
		t.Fatalf("重复作废应返回 404 且不追加事实，实际 %d", resp.StatusCode)
	}
	if resp, out := h.do(t, http.MethodGet, "/api/units", nil, RolePA); resp.StatusCode != http.StatusOK || len(out["units"].([]any)) != 0 {
		t.Fatalf("作废后列表不应包含该单元，实际 %d %v", resp.StatusCode, out)
	}
	if resp, out := h.do(t, http.MethodGet, statePath, nil, RolePA); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("作废后详情应返回 404，实际 %d %v", resp.StatusCode, out)
	}
	if resp, out := h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=retired", nil, RoleDA); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("作废后不得发起交付，实际 %d %v", resp.StatusCode, out)
	}
	if resp, out := h.do(t, http.MethodPost, "/api/tasks?pbc=retired", map[string]string{"title": "作废后新需求"}, RoleBA); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("作废后不得新建工作项，实际 %d %v", resp.StatusCode, out)
	}
	if resp, out := h.do(t, http.MethodPost, "/api/units", map[string]string{
		"id": "retired", "name": "改业务实现", "kind": "pbc", "profile": domain.ProfileMaterialInbound,
	}, RolePA); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("旧业务账本不能复用为另一种实现，实际 %d %v", resp.StatusCode, out)
	}
	registerUnit(t, h.st, "other")
	if resp, out := h.do(t, http.MethodPost, "/api/tasks?pbc=other", map[string]string{"title": "另一单元的工作项"}, RoleBA); resp.StatusCode != http.StatusCreated || out["id"] != "US-02" {
		t.Fatalf("别的单元不得占用历史编号，实际 %d %v", resp.StatusCode, out)
	}

	create("新登记")
	if resp, out := h.do(t, http.MethodDelete, path, nil, RolePA); resp.StatusCode != http.StatusConflict {
		t.Fatalf("旧登记的作废请求应返回 409，实际 %d %v", resp.StatusCode, out)
	}
	if resp, out := h.do(t, http.MethodGet, statePath, nil, RolePA); resp.StatusCode != http.StatusOK || out["name"] != "新登记" {
		t.Fatalf("重登后应读到新登记，实际 %d %v", resp.StatusCode, out)
	}
	after, err := h.st.Load(t.Context(), "retired")
	if err != nil || len(after) != len(before)+1 {
		t.Fatalf("重登应只追加单元登记事实：%+v，%v", after, err)
	}
	for i := range before {
		if before[i].Hash != after[i].Hash {
			t.Fatalf("原事实序号 %d 的摘要变化", before[i].Seq)
		}
	}
}

func TestCreateUnitRequiresPbcActAndAgentRef(t *testing.T) {
	h := newHarness(t)
	// DA 没有 pbc 权限。
	resp, _ := h.do(t, http.MethodPost, "/api/units",
		map[string]any{"id": "x1", "name": "n", "kind": "pbc"}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 建单元应 403，实际 %d", resp.StatusCode)
	}
	// agent 单元必须指向真实存在的 Agent。
	resp, _ = h.do(t, http.MethodPost, "/api/units",
		map[string]any{"id": "x1", "name": "n", "kind": "agent", "agentId": "nope"}, RolePA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知 Agent 应 400，实际 %d", resp.StatusCode)
	}
	seedAgent(t, h.st, goodAgent(), 48, 50)
	resp, out := h.do(t, http.MethodPost, "/api/units",
		map[string]any{"id": "x1", "name": "智能客服", "kind": "agent", "agentId": "x-fe"}, RolePA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PA 建 agent 单元应 201，实际 %d %v", resp.StatusCode, out)
	}
	// 同名再来一次 → 400。
	resp, _ = h.do(t, http.MethodPost, "/api/units",
		map[string]any{"id": "x1", "name": "又一个", "kind": "pbc"}, RolePA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("重复单元应 400，实际 %d", resp.StatusCode)
	}
}

func TestStateExposesAgentGatesForAgentUnit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	seedAgent(t, h.st, goodAgent(), 46, 50)
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "qa", "name": "质检", "kind": "agent", "agentId": "x-fe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Ensure(ctx, "qa"); err != nil {
		t.Fatal(err)
	}

	_, out := h.do(t, http.MethodGet, "/api/state?pbc=qa", nil, RoleBA)
	if out["kind"] != "agent" {
		t.Fatalf("state 应带出单元构成，实际 %v", out["kind"])
	}
	if out["agent"] == nil {
		t.Fatal("agent 单元必须带出 Agent 定义")
	}
	gates := out["gates"].(map[string]any)["all"].([]any)
	if len(gates) != 4 {
		t.Fatalf("Agent 单元应有四闸，实际 %v", gates)
	}
	// 46/50 = 92% < 95%，评测闸必须红。
	for _, g := range gates {
		m := g.(map[string]any)
		if m["key"] == "agent-eval" && m["ok"] == true {
			t.Fatalf("92%% 不许过评测闸：%v", m)
		}
	}
}

func TestHybridUnitNeedsSvcVerifyAndEval(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	seedAgent(t, h.st, goodAgent(), 50, 50)
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "dun", "name": "催缴", "kind": "hybrid", "agentId": "x-fe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Ensure(ctx, "dun"); err != nil {
		t.Fatal(err)
	}

	seedCommit(t, h.st, "dun")

	// 没验过服务侧 → svc-verify 闸红。
	_, out := h.do(t, http.MethodGet, "/api/state?pbc=dun", nil, RoleBA)
	all := out["gates"].(map[string]any)["all"].([]any)
	if len(all) != 8 {
		t.Fatalf("组合单元应是 3(服务)+1(验收)+4(Agent)=8 闸，实际 %d", len(all))
	}
	for _, g := range all {
		m := g.(map[string]any)
		if m["key"] == "svc-verify" && m["ok"] == true {
			t.Fatal("没验过服务侧时 svc-verify 不许通过")
		}
	}

	// DA（test）提交验收 20/22：仍未全过。
	resp, _ := h.do(t, http.MethodPost, "/api/units/dun/svcverify",
		map[string]any{"pass": 20, "total": 22}, RoleDA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("DA 提交验收应 201，实际 %d", resp.StatusCode)
	}
	_, out = h.do(t, http.MethodGet, "/api/state?pbc=dun", nil, RoleBA)
	for _, g := range out["gates"].(map[string]any)["all"].([]any) {
		m := g.(map[string]any)
		if m["key"] == "svc-verify" && m["ok"] == true {
			t.Fatal("20/22 未全过，svc-verify 不许变绿")
		}
	}

	// 22/22 才绿。
	h.do(t, http.MethodPost, "/api/units/dun/svcverify",
		map[string]any{"pass": 22, "total": 22}, RoleDA)
	_, out = h.do(t, http.MethodGet, "/api/state?pbc=dun", nil, RoleBA)
	ok := 0
	for _, g := range out["gates"].(map[string]any)["all"].([]any) {
		if g.(map[string]any)["ok"] == true {
			ok++
		}
	}
	if ok != 8 {
		t.Fatalf("两侧全过后应 8 闸全绿，实际 %d", ok)
	}
}

func TestAgentPublishRefusesBelowThreshold(t *testing.T) {
	h := newHarness(t)
	seedAgent(t, h.st, goodAgent(), 40, 50)
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-fe/publish", nil, RoleSA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("80%% 通过率不许发布，实际 %d %v", resp.StatusCode, out)
	}
	// 权限：DA 没有 tag。
	seedAgent(t, h.st, domain.Agent{ID: "x-ok", Name: "ok", Ver: "1.0.0", Model: "m",
		Prompt: "p", Tools: []string{"t"},
		Stages: []string{"done", "done", "done", "done", "done"}, Eval: [2]int{10, 10}}, 10, 10)
	resp, _ = h.do(t, http.MethodPost, "/api/agents/x-ok/publish", nil, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 发布 Agent 应 403，实际 %d", resp.StatusCode)
	}
	resp, out = h.do(t, http.MethodPost, "/api/agents/x-ok/publish", nil, RoleSA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("全过的 Agent 应 202，实际 %d %v", resp.StatusCode, out)
	}
}

func TestAgentRolloutIsStepwise(t *testing.T) {
	h := newHarness(t)
	a := domain.Agent{ID: "x-ok", Name: "ok", Ver: "1.0.0", Model: "m", Prompt: "p",
		Tools: []string{"t"}, Stages: []string{"done", "done", "done", "done", "done"}, Eval: [2]int{10, 10}}
	seedAgent(t, h.st, a, 10, 10)
	if resp, _ := h.do(t, http.MethodPost, "/api/agents/x-ok/publish", nil, RoleSA); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("发布应 202，实际 %d", resp.StatusCode)
	}
	// 跳档 10 → 100：不许。
	resp, _ := h.do(t, http.MethodPost, "/api/agents/x-ok/rollout", map[string]any{"pct": 100}, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("不能跳档，实际 %d", resp.StatusCode)
	}
	// 10 → 50 → 100：可以。
	if resp, _ := h.do(t, http.MethodPost, "/api/agents/x-ok/rollout", map[string]any{"pct": 50}, RoleDA); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("10→50 应 202，实际 %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodPost, "/api/agents/x-ok/rollout", map[string]any{"pct": 100}, RoleDA); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("50→100 应 202，实际 %d", resp.StatusCode)
	}
	// 回退档位：不许（回滚用另一个接口）。
	if resp, _ := h.do(t, http.MethodPost, "/api/agents/x-ok/rollout", map[string]any{"pct": 50}, RoleDA); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("放量不许回退档位，实际 %d", resp.StatusCode)
	}
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-ok/rollback", nil, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("回滚应 202，实际 %d %v", resp.StatusCode, out)
	}
}

func TestAgentEvalRefusesToInventResults(t *testing.T) {
	h := newHarness(t)
	seedAgent(t, h.st, goodAgent(), 46, 50)
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-fe/eval",
		map[string]any{"pass": 5}, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺 total 必须 400，实际 %d %v", resp.StatusCode, out)
	}
	resp, _ = h.do(t, http.MethodPost, "/api/agents/x-fe/eval",
		map[string]any{"pass": 48, "total": 50, "ver": "1.4.3"}, RoleDA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("合法结果应 201，实际 %d", resp.StatusCode)
	}
	_, out = h.do(t, http.MethodGet, "/api/agents/x-fe", nil, RoleBA)
	if out["eval"].([]any)[0].(float64) != 48 {
		t.Fatalf("评测结果应进投影，实际 %v", out["eval"])
	}
}

func TestBRDSaveRequiresBAAndVersionsLine(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodPost, "/api/brd", map[string]any{"text": "# x"}, RolePA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("PA 存 BRD 应 403，实际 %d", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodPost, "/api/brd", map[string]any{"text": "  "}, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空 BRD 应 400，实际 %d", resp.StatusCode)
	}
	resp, out := h.do(t, http.MethodPost, "/api/brd", map[string]any{"text": "# 业务背景", "note": "初版"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("BA 存 BRD 应 201，实际 %d %v", resp.StatusCode, out)
	}
	_, got := h.do(t, http.MethodGet, "/api/brd", nil, RolePA)
	if got["v"] != out["v"] || got["text"] != "# 业务背景" {
		t.Fatalf("BRD 投影不符：%v", got)
	}
	// 再存一次：版本号递增，旧草稿变 superseded。
	resp, out2 := h.do(t, http.MethodPost, "/api/brd", map[string]any{"text": "# v2"}, RoleBA)
	if resp.StatusCode != http.StatusCreated || out2["v"] == out["v"] {
		t.Fatalf("第二次保存应递增版本：%v → %v", out["v"], out2["v"])
	}
	_, got = h.do(t, http.MethodGet, "/api/brd", nil, RoleBA)
	vers := got["versions"].([]any)
	if len(vers) != 2 || vers[0].(map[string]any)["st"] != "superseded" {
		t.Fatalf("旧草稿应 superseded：%v", vers)
	}
}

func TestNotificationSummaryComesFromFacts(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "fee", "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "new", Env: "test", Mode: "fast", Run: "waiting", Step: 6, By: "BA"})
	if _, err := h.st.Append(ctx, "fee", store.FactItemPublished, "SA", map[string]any{
		"id": "US-01", "tag": "v1.0.0", "version": "1.0.2", "url": "https://x",
	}); err != nil {
		t.Fatal(err)
	}
	resp, out := h.do(t, http.MethodGet, "/api/notifications", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d", resp.StatusCode)
	}
	notes := out["notifications"].([]any)
	if len(notes) == 0 {
		t.Fatal("已发布的工作项应产生一条通知")
	}
	b, _ := json.Marshal(notes[0])
	if !strings.Contains(string(b), "收费缴费") {
		t.Fatalf("通知文案应带单元名：%s", b)
	}
}

// seedCommit 写一棵完整的制品树 + 一次提交，让服务侧四闸有东西可判。
func seedCommit(t *testing.T, st *store.Store, pbc string) {
	t.Helper()
	ctx := t.Context()
	tree := domain.Tree{}
	for _, t2 := range domain.Order {
		tree[t2] = "1.0.1"
	}
	for _, t2 := range domain.Order {
		v := domain.Version{T: t2, V: "1.0.1", At: "11-20 17:20", By: "x-fe",
			Source: "生成", St: domain.StAccepted, DF: domain.DeriveRefs(t2, func(a domain.Artifact) string {
				return "1.0.1"
			})}
		if _, err := st.Append(ctx, pbc, store.FactArtifactDrafted, v.By, v); err != nil {
			t.Fatal(err)
		}
	}
	c := domain.Commit{No: "1.0.1", Tree: tree, By: "张昆", At: "11-20 17:20",
		Type: "new", TreeSHA: domain.TreeSHA(tree), Digest: "sha256:seed"}
	if _, err := st.Append(ctx, pbc, store.FactCommitCreated, "i3-pipeline", c); err != nil {
		t.Fatal(err)
	}
}

func TestSaveDraftCreatesNewVersionWithContent(t *testing.T) {
	h := newHarness(t)
	registerUnit(t, h.st, "fee") // #43：/api/state 只认登记过的单元
	seedCommit(t, h.st, "fee")   // v1.0.1 全树 accepted
	// BA 写 PRD：允许。
	resp, out := h.do(t, http.MethodPost, "/api/artifacts",
		map[string]any{"artifact": "prd", "text": "# 新需求", "note": "补 §4"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("BA 存 PRD 应 201，实际 %d %v", resp.StatusCode, out)
	}
	if out["version"] != "1.0.2" {
		t.Fatalf("新草稿应是 1.0.2，实际 %v", out["version"])
	}
	// DA 写 PRD：拒绝（没有产品侧动作）。
	resp, _ = h.do(t, http.MethodPost, "/api/artifacts",
		map[string]any{"artifact": "prd", "text": "# x"}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 存 PRD 应 403，实际 %d", resp.StatusCode)
	}
	// 空内容：拒绝。
	resp, _ = h.do(t, http.MethodPost, "/api/artifacts",
		map[string]any{"artifact": "prd", "text": "  "}, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空内容应 400，实际 %d", resp.StatusCode)
	}
	// 内容随版本进投影：GET /api/state 能读到 md。
	_, st := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	prd := st["versions"].(map[string]any)["prd"].([]any)
	last := prd[len(prd)-1].(map[string]any)
	if last["md"] != "# 新需求" || last["st"] != "draft" {
		t.Fatalf("草稿内容必须进投影：%v", last)
	}
}

func TestSaveCodeDraftKeepsFiles(t *testing.T) {
	h := newHarness(t)
	seedCommit(t, h.st, "fee")
	resp, out := h.do(t, http.MethodPost, "/api/artifacts",
		map[string]any{"artifact": "code", "files": map[string]string{"a.go": "package a"}}, RoleDA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("DA 存代码草稿应 201，实际 %d %v", resp.StatusCode, out)
	}
	resp, _ = h.do(t, http.MethodPost, "/api/artifacts",
		map[string]any{"artifact": "code"}, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("没有 files 应 400，实际 %d", resp.StatusCode)
	}
}

// #43：/api/state 不许回落某个默认单元，也不许把「不存在的 id」答成 200 空投影。
func TestStateRequiresKnownPBC(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "fee", "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}

	// 不带 pbc：以前回 fee 的完整账本并 200，看上去一切正常——这正是要禁止的。
	resp, out := h.do(t, http.MethodGet, "/api/state", nil, RoleBA)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(errText(out), "?pbc=") {
		t.Fatalf("不带 pbc 应 400 并说明需要 ?pbc=，实际 %d %s", resp.StatusCode, errText(out))
	}
	// 空值同样不行：空串不是「用默认」，是「没给」。
	resp, out = h.do(t, http.MethodGet, "/api/state?pbc=", nil, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pbc 为空应 400，实际 %d %s", resp.StatusCode, errText(out))
	}
	// 注册表里没有的 id：404，与 /api/replay 一致。
	resp, out = h.do(t, http.MethodGet, "/api/state?pbc=no-such-unit", nil, RoleBA)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(errText(out), "no-such-unit") {
		t.Fatalf("不存在的单元应 404 并点名，实际 %d %s", resp.StatusCode, errText(out))
	}
	// 在注册表里、日志还空：200 + 空投影（「刚建好还没事实」）。
	resp, out = h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("已登记的单元应 200，实际 %d %s", resp.StatusCode, errText(out))
	}
}

func errText(v map[string]any) string {
	s, _ := v["error"].(string)
	return s
}

// 页面只要最近的事实流：?limit=N 回最新 N 条（新的在前），不带 limit 仍回全量。
// 全量每轮 refresh 走一遍会让权限位排队等它（#46）。
func TestFactsEndpointHonoursLimit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "fee", "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})
	seedItem(t, h.st, store.Item{ID: "US-02", Title: "u", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	resp, out := h.do(t, http.MethodGet, "/api/facts?pbc=fee&limit=2", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d %v", resp.StatusCode, out)
	}
	top := out["facts"].([]any)
	if len(top) != 2 {
		t.Fatalf("limit=2 应回两条，实际 %d", len(top))
	}
	// 账本里这条测试写入的最新事实是第二条 work item。
	if top[0].(map[string]any)["type"] != "item.created" {
		t.Fatalf("新的在前：limit 截断后第一条仍是最新那条，实际 %v", top[0])
	}
	_, all := h.do(t, http.MethodGet, "/api/facts?pbc=fee", nil, RoleBA)
	if n := len(all["facts"].([]any)); n != 2 {
		t.Fatalf("不带 limit 应回全量（两条 work item），实际 %d", n)
	}
}
