package bff

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// taggedUnit 造一个可打 tag 的单元：完整制品树 + 一次提交 + 测试全过 +
// 针对这次提交的验收清单 + 登记。
//
// 清单不能省：没有清单就不许打 tag（#70），夹具少了这一条，
// 后面每一个「打 tag 应成功」的用例都变成了在验错误的前提。
func taggedUnit(t *testing.T, h *harness, id string) {
	t.Helper()
	ctx := t.Context()
	seedCommit(t, h.st, id)
	if _, err := h.st.Append(ctx, id, store.FactTestRun, "x-qa", map[string]any{
		"commit": "1.0.1", "pass": 5, "total": 5, "by": "x-qa",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, id, store.FactCheckDefined, "x-qa", map[string]any{
		"commit": "1.0.1",
		"items": []domain.Check{
			{ID: "AC-1", N: "健康检查可用", ST: domain.CheckPass, Commit: "1.0.1"},
			{ID: "AC-2", N: "列表可读", ST: domain.CheckPass, Commit: "1.0.1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": id, "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
}

// 打 tag 成功时，闸门要定格成事实；回放能读到「当时」的那组闸。
func TestTagRecordsGateEvaluation(t *testing.T) {
	h := newHarness(t)
	taggedUnit(t, h, "fee")

	resp, out := h.do(t, http.MethodPost, "/api/units/fee/tag", nil, RoleSA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("SA 打 tag 应 201，实际 %d %v", resp.StatusCode, out)
	}
	tag, _ := out["tag"].(string)
	if tag == "" {
		t.Fatal("应返回 tag 号")
	}

	// 账本里 gate.evaluated 紧挨着 tag.created：先证据、后凭据。
	_, fl := h.do(t, http.MethodGet, "/api/facts?pbc=fee", nil, RoleSA)
	facts, _ := fl["facts"].([]any)
	// /api/facts 新的在前。
	var order []string
	for _, raw := range facts {
		order = append(order, raw.(map[string]any)["type"].(string))
	}
	if len(order) < 2 {
		t.Fatalf("至少应有两条事实（闸 + tag），实际 %v", order)
	}
	if order[1] != store.FactGateEvaluated || order[0] != store.FactTagCreated {
		t.Fatalf("应是 gate.evaluated → tag.created 紧邻，实际前两条 %v", order[:2])
	}

	resp, rv := h.do(t, http.MethodGet, "/api/units/fee/replay?tag="+tag, nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("已打 tag 的单元回放应 200，实际 %d %v", resp.StatusCode, rv)
	}
	gates, _ := rv["gates"].([]any)
	if len(gates) != 4 {
		t.Fatalf("回放应给出当时那四闸，实际 %v", rv["gates"])
	}
	if rv["gatesNote"] != nil {
		t.Fatalf("有落账的闸就不该给「没有记录」的说明：%v", rv["gatesNote"])
	}
	// 回放要带上打 tag 那一刻的清单，并且说清它验的是哪个提交（#70）。
	checks, ok := rv["checks"].([]any)
	if !ok || len(checks) != 2 {
		t.Fatalf("回放应带上当时的两条验收，实际 %v", rv["checks"])
	}
	if c, _ := checks[0].(map[string]any); c["commit"] != "1.0.1" {
		t.Fatalf("回放的验收要绑定当时的提交，实际 %v", checks[0])
	}
	if _, ok := rv["facts"].([]any); !ok {
		t.Fatalf("回归窗口的事实应是数组，实际 %v", rv["facts"])
	}
	if test, _ := rv["test"].(map[string]any); test == nil || test["total"].(float64) != 5 {
		t.Fatalf("回放要带上当时的测试结论，实际 %v", rv["test"])
	}
	if rv["commit"] != "1.0.1" || rv["tag"] != tag {
		t.Fatalf("回放的 tag/commit 不对：%v", rv)
	}
}

// 回放之后账本又有新事实：全量投影变了，回放窗口不变。
func TestReplayIgnoresFactsAfterTag(t *testing.T) {
	h := newHarness(t)
	taggedUnit(t, h, "fee")
	resp, out := h.do(t, http.MethodPost, "/api/units/fee/tag", nil, RoleSA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("打 tag 失败：%d %v", resp.StatusCode, out)
	}
	tag, _ := out["tag"].(string)

	// tag 之后再来一次提交。
	seedNewCommit(t, h.st, "fee", "1.0.2")

	resp, rv := h.do(t, http.MethodGet, "/api/units/fee/replay?tag="+tag, nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回放应 200，实际 %d %v", resp.StatusCode, rv)
	}
	if rv["commit"] != "1.0.1" {
		t.Fatalf("回放应停在打 tag 那次提交 1.0.1，实际 %v", rv["commit"])
	}
	commits, _ := rv["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("回放窗口里只该有 1 次提交，实际 %d", len(commits))
	}

	// 而此刻的全量投影已经前进到 1.0.2。
	_, st := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	cur, _ := st["commits"].([]any)
	if len(cur) == 0 || cur[0].(map[string]any)["no"] != "1.0.2" {
		t.Fatalf("现在的投影应是 1.0.2，实际 %v", cur)
	}
}

// 不指定 tag 时取最近一次；单元不存在 404；没打过 tag 400 且说清是哪种。
func TestReplayErrorCases(t *testing.T) {
	h := newHarness(t)
	taggedUnit(t, h, "fee")
	if resp, out := h.do(t, http.MethodPost, "/api/units/fee/tag", nil, RoleSA); resp.StatusCode != http.StatusCreated {
		t.Fatalf("打 tag 失败：%v %v", resp.StatusCode, out)
	}

	// 不指定 tag：默认最近一次。
	resp, rv := h.do(t, http.MethodGet, "/api/units/fee/replay", nil, RoleBA)
	if resp.StatusCode != http.StatusOK || rv["tag"] == "" {
		t.Fatalf("不指定 tag 时应回放最近一次，实际 %d %v", resp.StatusCode, rv)
	}

	// 单元不存在 → 404。
	resp, out := h.do(t, http.MethodGet, "/api/units/nope/replay", nil, RoleBA)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的单元应 404，实际 %d %v", resp.StatusCode, out)
	}

	// 单元存在但没打过 tag → 400，且说清是「还没打」。
	if err := h.st.Ensure(t.Context(), unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(t.Context(), unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "raw", "name": "还没交付", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
	resp, out = h.do(t, http.MethodGet, "/api/units/raw/replay", nil, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("没打过 tag 的单元应 400，实际 %d %v", resp.StatusCode, out)
	}
	if msg, _ := out["error"].(string); msg == "" || !strings.Contains(msg, "还没打过 tag") {
		t.Fatalf("400 要说清是「还没打 tag」，实际 %q", msg)
	}

	// 指定了不存在的 tag → 400，并把可选 tag 列出来。
	resp, out = h.do(t, http.MethodGet, "/api/units/fee/replay?tag=v9.9.9", nil, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("不存在的 tag 应 400，实际 %d %v", resp.StatusCode, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "v9.9.9") || !strings.Contains(msg, "v1.0.0") {
		t.Fatalf("400 要点名缺哪个 tag、有哪些可选，实际 %q", msg)
	}
}

// 「当时的闸没落账」时如实说没有，不拿今天的账本重算冒充当时。
func TestReplayWithoutGateFactSaysSo(t *testing.T) {
	h := newHarness(t)
	// 手工落一个 tag.created，不带 gate.evaluated——模拟升级前的老账本。
	ctx := t.Context()
	seedCommit(t, h.st, "fee")
	if _, err := h.st.Append(ctx, "fee", store.FactTagCreated, "seed", map[string]any{
		"tag": "v1.0.0", "commit": "1.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "fee", "name": "收费缴费", "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}

	resp, rv := h.do(t, http.MethodGet, "/api/units/fee/replay", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回放应 200，实际 %d %v", resp.StatusCode, rv)
	}
	if rv["gates"] != nil {
		t.Fatalf("没有落账就不该给出闸门，实际 %v", rv["gates"])
	}
	note, _ := rv["gatesNote"].(string)
	if !strings.Contains(note, "没有记录") {
		t.Fatalf("要说明「当时没记录闸门」，实际 %q", note)
	}
}

func seedNewCommit(t *testing.T, st *store.Store, pbc, no string) {
	t.Helper()
	ctx := t.Context()
	tree := map[domain.Artifact]string{}
	for _, a := range domain.Order {
		tree[a] = "1.0.1"
	}
	c := domain.Commit{No: no, Tree: tree, By: "x-fe", At: "11-26 12:00", Type: "feature",
		TreeSHA: domain.TreeSHA(tree), Digest: "sha256:seed"}
	if _, err := st.Append(ctx, pbc, store.FactCommitCreated, "x-fe", c); err != nil {
		t.Fatal(err)
	}
}
