package domain

import "testing"

func line(vs ...Version) map[Artifact][]Version {
	m := map[Artifact][]Version{}
	for _, v := range vs {
		m[v.T] = append(m[v.T], v)
	}
	return m
}

func ver(t Artifact, v, st string, df ...Ref) Version {
	return Version{T: t, V: v, St: st, DF: df}
}

func TestDeriveRefsMatchesPrototypeRules(t *testing.T) {
	latest := func(a Artifact) string { return "9.9.9" }
	cases := map[Artifact]int{PRD: 0, Proto: 1, AD: 2, AT: 2, Backlog: 1, Code: 2}
	for t2, n := range cases {
		if got := DeriveRefs(t2, latest); len(got) != n {
			t.Errorf("%s 的衍生引用数=%d，期望 %d", t2, len(got), n)
		}
	}
	if d := DeriveRefs(AD, latest); d[0].T != PRD || d[1].T != Proto {
		t.Errorf("AD 应依序引用 PRD、Proto，实际 %+v", d)
	}
}

func TestStaleFollowsLatestOnly(t *testing.T) {
	// AD 引用 PRD 1.0.1，但 PRD 已经到 1.0.2 → AD 过期。
	m := line(
		ver(PRD, "1.0.1", StAccepted),
		ver(PRD, "1.0.2", StDraft),
		ver(AD, "1.0.1", StDraft, Ref{PRD, "1.0.1"}),
	)
	st := Stale(m)
	if len(st) != 1 || st[0].T != AD {
		t.Fatalf("期望只有 AD 过期，实际 %+v", st)
	}
}

func TestStaleEmptyWhenUpstreamUnchanged(t *testing.T) {
	m := line(
		ver(PRD, "1.0.1", StAccepted),
		ver(Proto, "1.0.1", StDraft, Ref{PRD, "1.0.1"}),
	)
	if st := Stale(m); len(st) != 0 {
		t.Fatalf("不应有过期制品，实际 %+v", st)
	}
}

func TestNextVersion(t *testing.T) {
	cases := map[string]string{"": "1.0.1", "1.0.1": "1.0.2", "2.3.9": "2.3.10"}
	for in, want := range cases {
		got, err := NextVersion(in)
		if err != nil || got != want {
			t.Errorf("NextVersion(%q)=(%q,%v)，期望 %q", in, got, err, want)
		}
	}
	if _, err := NextVersion("abc"); err == nil {
		t.Error("非法版本号应当报错")
	}
}

func TestNextTag(t *testing.T) {
	if got := NextTag(nil); got != "v1.0.0" {
		t.Errorf("空 tag 列表应得 v1.0.0，实际 %s", got)
	}
	if got := NextTag([]string{"v1.0.0", "v1.0.2"}); got != "v1.0.3" {
		t.Errorf("应得 v1.0.3，实际 %s", got)
	}
}

func TestCheckCommitFourGates(t *testing.T) {
	tree := Tree{PRD: "1.0.1", Proto: "1.0.1", AD: "1.0.1", AT: "1.0.1", Backlog: "1.0.1", Code: "1.0.1"}
	m := line(
		ver(PRD, "1.0.1", StAccepted),
		ver(Proto, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}),
		ver(AD, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}, Ref{Proto, "1.0.1"}),
		ver(AT, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}, Ref{AD, "1.0.1"}),
		ver(Backlog, "1.0.1", StAccepted, Ref{AT, "1.0.1"}),
		ver(Code, "1.0.1", StAccepted, Ref{AT, "1.0.1"}, Ref{Backlog, "1.0.1"}),
	)

	// 全部就绪：只剩 digest 与视觉差异两道闸。
	c := Commit{No: "1.0.1", Tree: tree}
	g := CheckCommit(c, m, 0)
	if !g[0].OK || !g[1].OK {
		t.Fatalf("引用闭合与 accepted 应通过：%+v", g)
	}
	if g[2].OK {
		t.Error("未构建时 digest 闸不应通过")
	}
	if !GatesPass(CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, 0)) {
		t.Error("四闸齐备时应当通过")
	}
	if GatesPass(CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, 3)) {
		t.Error("有未处理视觉差异时不应通过")
	}

	// 引用不闭合：AD 引用的 Proto 版本与树不一致。
	bad := map[Artifact]string{}
	for k, v := range tree {
		bad[k] = v
	}
	m2 := line(
		ver(PRD, "1.0.1", StAccepted),
		ver(Proto, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}),
		ver(AD, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}, Ref{Proto, "0.9.9"}),
		ver(AT, "1.0.1", StAccepted, Ref{PRD, "1.0.1"}, Ref{AD, "1.0.1"}),
		ver(Backlog, "1.0.1", StAccepted, Ref{AT, "1.0.1"}),
		ver(Code, "1.0.1", StAccepted, Ref{AT, "1.0.1"}, Ref{Backlog, "1.0.1"}),
	)
	if g := CheckCommit(Commit{No: "1.0.1", Tree: bad}, m2, 0); g[0].OK {
		t.Error("悬空引用时引用闭合闸不应通过")
	}

	// 草稿进树：accepted 闸不通过。
	m3 := line(ver(PRD, "1.0.1", StDraft))
	if g := CheckCommit(Commit{No: "1.0.1", Tree: Tree{PRD: "1.0.1"}}, m3, 0); g[1].OK {
		t.Error("草稿入树时 accepted 闸不应通过")
	}
}

func TestTreeSHAIsOrderIndependent(t *testing.T) {
	a := Tree{PRD: "1.0.1", AD: "1.0.1", Code: "1.0.1"}
	b := Tree{Code: "1.0.1", PRD: "1.0.1", AD: "1.0.1"}
	if TreeSHA(a) != TreeSHA(b) {
		t.Error("树摘要必须与 map 迭代顺序无关")
	}
	if TreeSHA(a) == TreeSHA(Tree{PRD: "1.0.2", AD: "1.0.1", Code: "1.0.1"}) {
		t.Error("树内容不同，摘要必须不同")
	}
}
