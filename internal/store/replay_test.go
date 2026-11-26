package store

import (
	"encoding/json"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// tagTree 造一本最小账：一次提交 → 一条测试 → 一条闸 → 一个 tag → 之后的改动。
//
// 它故意把「tag 之后的新事实」摆在窗口右边，验证 as-of 不会把后来的变化
// 混进「当时」。
func tagTree(t *testing.T) []Fact {
	t.Helper()
	mk := func(seq int, typ string, payload any) Fact {
		t.Helper()
		return factOf(t, seq, typ, payload)
	}
	gates := []domain.Gate{
		{Key: "closure", Label: "引用闭合", OK: true, Detail: "无悬空引用"},
		{Key: "accepted", Label: "制品全部 accepted", OK: true, Detail: "无草稿"},
		{Key: "digest", Label: "镜像与树一致", OK: true, Detail: "sha256:abc"},
		{Key: "visual", Label: "原型还原", OK: true, Unknown: true, Detail: "判不了"},
	}
	return []Fact{
		mk(1, FactPBCCreated, map[string]any{"name": "收费缴费"}),
		mk(2, FactCommitCreated, map[string]any{
			"no": "1.0.1", "tree": map[string]any{"prd": "1.0.1"}, "by": "x-fe",
			"at": "11-26 10:00", "msg": "首次交付", "type": "new", "treeSha": "sha256:tree",
			"digest": "sha256:abc",
		}),
		mk(3, FactTestRun, TestResult{Commit: "1.0.1", Pass: 5, Total: 5, At: "11-26 10:01", By: "x-qa"}),
		mk(4, FactGateEvaluated, GateEvaluation{
			Tag: "v1.0.0", Commit: "1.0.1", Gates: gates,
			Checks: CheckSummary{Total: 2, Pass: 2},
			Test:   &TestResult{Commit: "1.0.1", Pass: 5, Total: 5},
			By:     "SA",
		}),
		mk(5, FactTagCreated, map[string]any{"tag": "v1.0.0", "commit": "1.0.1", "digest": "sha256:abc"}),
		// 以下是 tag 之后发生的事：不应出现在 as-of 视图里。
		mk(6, FactCommitCreated, map[string]any{
			"no": "1.0.2", "tree": map[string]any{"prd": "1.0.2"}, "by": "x-fe",
			"at": "11-26 11:00", "msg": "后续改动", "type": "feature", "treeSha": "sha256:tree2",
		}),
		mk(7, FactTestRun, TestResult{Commit: "1.0.2", Pass: 4, Total: 6, At: "11-26 11:01", By: "x-qa"}),
	}
}

// 同一本账：tag 之后没有新增事实时，as-of 与全量投影一致；
// 有新增时，as-of 不含后来的变化。
func TestProjectAsOfSlicesAtTag(t *testing.T) {
	facts := tagTree(t)

	// 全量投影：HEAD 已经是 1.0.2。
	full, err := Project("fee", facts)
	if err != nil {
		t.Fatal(err)
	}
	if full.Commits[0].No != "1.0.2" {
		t.Fatalf("全量投影应有后来的提交，实际 %q", full.Commits[0].No)
	}

	// 截到 tag.created（seq=5）：HEAD 还是 1.0.1，测试是 5/5，tag 在。
	as, err := ProjectAsOf("fee", facts, 5)
	if err != nil {
		t.Fatal(err)
	}
	if as.Commits[0].No != "1.0.1" {
		t.Fatalf("as-of 不该含 tag 之后的提交，实际 %q", as.Commits[0].No)
	}
	if len(as.Commits) != 1 {
		t.Fatalf("as-of 应只有 1 次提交，实际 %d", len(as.Commits))
	}
	if as.Test == nil || as.Test.Total != 5 || as.Test.Pass != 5 {
		t.Fatalf("as-of 的测试应是当时那次 5/5，实际 %+v", as.Test)
	}
	if len(as.Tags) != 1 || as.Tags[0] != "v1.0.0" {
		t.Fatalf("as-of 应已含 tag，实际 %v", as.Tags)
	}
	if as.Commits[0].Tag != "v1.0.0" {
		t.Fatalf("as-of 的 HEAD 上应挂着 tag，实际 %+v", as.Commits[0])
	}
	if as.Seq != 5 {
		t.Fatalf("Seq 应等于窗口长度 5，实际 %d", as.Seq)
	}

	// 边界：截到 tag 之前（4）——闸已落账、tag 还没落，不能出现 tag。
	pre, err := ProjectAsOf("fee", facts, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre.Tags) != 0 {
		t.Fatalf("截到 tag 之前不该有 tag，实际 %v", pre.Tags)
	}
	if len(pre.GateEvals) != 1 {
		t.Fatalf("截到 tag 之前应已能看到那条闸，实际 %d", len(pre.GateEvals))
	}

	// 与全量一致的那条约定：截到账本末尾，结果必须与 Project 完全相同。
	tail, err := ProjectAsOf("fee", facts, len(facts))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(full)
	b, _ := json.Marshal(tail)
	if string(a) != string(b) {
		t.Fatalf("截到末尾时 as-of 应与全量投影一致：\n%s\n%s", a, b)
	}
}

// gate.evaluated 可解析出四闸与验收/测试摘要。
func TestGateEvaluatedIsParsable(t *testing.T) {
	facts := tagTree(t)
	v, err := Project("fee", facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.GateEvals) != 1 {
		t.Fatalf("应有一条闸门事实，实际 %d", len(v.GateEvals))
	}
	e := v.GateEvals[0]
	if e.Tag != "v1.0.0" || e.Commit != "1.0.1" {
		t.Fatalf("闸门事实的 tag/commit 不对：%+v", e)
	}
	if len(e.Gates) != 4 {
		t.Fatalf("四闸应完整落账，实际 %d：%+v", len(e.Gates), e.Gates)
	}
	var unknown int
	for _, g := range e.Gates {
		if g.Unknown {
			unknown++
		}
	}
	if unknown != 1 || e.Gates[3].Key != "visual" {
		t.Fatalf("「判不了」的闸要保留 Unknown，不能被当成通过：%+v", e.Gates)
	}
	if e.Checks.Total != 2 || e.Checks.Pass != 2 {
		t.Fatalf("验收摘要不对：%+v", e.Checks)
	}
	if e.Test == nil || e.Test.Pass != 5 || e.Test.Total != 5 {
		t.Fatalf("测试摘要不对：%+v", e.Test)
	}
	if e.By != "SA" {
		t.Fatalf("要记下是谁按的：%q", e.By)
	}
}

// UpTo 不编造：seq 超出账本长度只给已有部分，seq<=0 给空。
func TestUpToNeverInventsFacts(t *testing.T) {
	facts := tagTree(t)
	if got := UpTo(facts, 0); len(got) != 0 {
		t.Fatalf("seq=0 应为空，实际 %d 条", len(got))
	}
	if got := UpTo(facts, len(facts)+10); len(got) != len(facts) {
		t.Fatalf("越界应只给已有部分，实际 %d 条", len(got))
	}
}
