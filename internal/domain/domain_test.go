package domain

import (
	"fmt"
	"strings"
	"testing"
)

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
	// 每个用例都断言「结果大于列表里最大的那个」——这是 NextTag 唯一不能破的承诺，
	// 因为 tag 是交付凭据，倒退会撞号。硬编码期望值只是把承诺写得更具体。
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"空列表", nil, "v1.0.0"},
		{"只有非法字符串", []string{"abc", "", "v1"}, "v1.0.0"},
		{"同 minor 取最大 patch", []string{"v1.0.0", "v1.0.2"}, "v1.0.3"},
		{"乱序输入", []string{"v1.0.2", "v1.0.0", "v1.0.1"}, "v1.0.3"},
		{"跨 minor", []string{"v1.0.0", "v1.2.0"}, "v1.2.1"},
		{"跨 major", []string{"v2.0.0"}, "v2.0.1"},
		{"两位 minor", []string{"v1.10.0"}, "v1.10.1"},
		{"major 大但 patch 小", []string{"v2.0.0", "v1.0.9"}, "v2.0.1"},
		{"非法字符串夹在中间", []string{"abc", "v1.0.1", "v3.4"}, "v1.0.2"},
	}
	for _, c := range cases {
		got := NextTag(c.in)
		if got != c.want {
			t.Errorf("%s：NextTag(%v) = %s，期望 %s", c.name, c.in, got, c.want)
		}
		for _, tag := range c.in {
			var a, b, p int
			if n, err := fmt.Sscanf(tag, "v%d.%d.%d", &a, &b, &p); n == 3 && err == nil {
				var x, y, z int
				if _, err2 := fmt.Sscanf(got, "v%d.%d.%d", &x, &y, &z); err2 != nil {
					t.Fatalf("%s：返回值 %s 自己都解析不了", c.name, got)
				}
				if lessTuple([3]int{x, y, z}, [3]int{a, b, p}) {
					t.Errorf("%s：结果 %s 比已有 tag %s 小，会撞号", c.name, got, tag)
				}
			}
		}
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
	g := CheckCommit(c, m, intPtr(0))
	if !g[0].OK || !g[1].OK {
		t.Fatalf("引用闭合与 accepted 应通过：%+v", g)
	}
	if g[2].OK {
		t.Error("未构建时 digest 闸不应通过")
	}
	if !GatesPass(CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, intPtr(0))) {
		t.Error("四闸齐备时应当通过")
	}
	if GatesPass(CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, intPtr(3))) {
		t.Error("有未处理视觉差异时不应通过")
	}
	// 没比对过 ≠ 比对过没差异：前者这条闸判不了，不能说它过了。
	unknown := CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, nil)
	if !unknown[3].Unknown {
		t.Error("没有视觉比对结果时，这条闸应标成判不了")
	}
	if !unknown[3].OK {
		t.Error("判不了不该挡住发布（它不表示没过）")
	}
	if CheckCommit(Commit{No: "1.0.1", Tree: tree, Digest: "sha256:x"}, m, intPtr(0))[3].Unknown {
		t.Error("真的比对过之后就不该再标判不了")
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
	if g := CheckCommit(Commit{No: "1.0.1", Tree: bad}, m2, intPtr(0)); g[0].OK {
		t.Error("悬空引用时引用闭合闸不应通过")
	}

	// 草稿进树：accepted 闸不通过。
	m3 := line(ver(PRD, "1.0.1", StDraft))
	if g := CheckCommit(Commit{No: "1.0.1", Tree: Tree{PRD: "1.0.1"}}, m3, intPtr(0)); g[1].OK {
		t.Error("草稿入树时 accepted 闸不应通过")
	}
}

// intPtr 给 CheckCommit 的「视觉差异数」传一个真的比过的值。
func intPtr(n int) *int { return &n }

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

func TestAgentGatesRequireNinetyFivePercent(t *testing.T) {
	base := func() *Agent {
		a := &Agent{ID: "x-fe", Ver: "1.0.0", Prompt: "p", Model: "m",
			Stages: []string{"done", "done", "done", "done", "todo"},
			Tools:  []string{"repo.read"}, Eval: [2]int{19, 20}}
		// 构建过一次：BuiltDigest 与当前定义算出的 digest 相同。
		a.Digest = AgentDigest(*a)
		a.BuiltDigest = a.Digest
		return a
	}
	if gs := AgentGates(base()); !GatesPass(gs) {
		t.Fatalf("19/20 = 95%% 应全过，实际 %+v", gs)
	}
	a := base()
	a.Eval = [2]int{18, 20}
	gs := AgentGates(a)
	if GatesPass(gs) {
		t.Fatal("18/20 = 90% 不许发布")
	}
	if gs[3].OK || gs[3].Key != "agent-eval" {
		t.Fatalf("应该是评测闸不过：%+v", gs[3])
	}
}

func TestAgentGatesRejectUnbuiltOrStaleEval(t *testing.T) {
	// 定义改过但没构建：digest 对不上，闸必须红。
	a := &Agent{ID: "x", Ver: "1.0.0", Prompt: "p", Model: "m", BuiltDigest: "sha256:old",
		Stages: []string{"done", "done", "done", "done", "done"},
		Tools:  []string{"t"}, Eval: [2]int{10, 10}}
	gs := AgentGates(a)
	if gs[2].OK {
		t.Fatalf("编排改了没构建，digest 闸应红：%+v", gs[2])
	}
	// 评测跑在旧 digest 上：旧分数不能给新版本背书。
	a.BuiltDigest = a.Digest
	a.Runs = []EvalRun{{ID: "e1", Pass: 10, Total: 10, Digest: "sha256:other"}}
	if gs := AgentGates(a); gs[3].OK {
		t.Fatalf("评测针对旧版本时评测闸应红：%+v", gs[3])
	}
	// 同一 digest 的评测才算数。
	a.Runs[0].Digest = a.BuiltDigest
	if gs := AgentGates(a); !GatesPass(gs) {
		t.Fatalf("同 digest 的评测应全过：%+v", gs)
	}
}

func TestFlowValidAndToolWiring(t *testing.T) {
	f := &Flow{
		Nodes: []FlowNode{
			{ID: "n1", N: "任务进入", T: NodeStart},
			{ID: "n2", N: "读会话", T: NodeTool, Cfg: map[string]any{"tool": "session.read"}},
			{ID: "n3", N: "判定", T: NodeLLM},
			{ID: "n4", N: "回执", T: NodeEnd},
		},
		Edges: [][3]string{{"n1", "n2", ""}, {"n2", "n3", ""}, {"n3", "n4", ""}},
	}
	if err := f.Valid(); err != nil {
		t.Fatalf("合法编排被拒：%v", err)
	}
	a := &Agent{Tools: []string{"session.read", "report.write"}, Flow: f}
	w := ToolWiring(a)
	if !w[0].Wired || w[1].Wired {
		t.Fatalf("接线检查不对：%+v", w)
	}

	bad := &Flow{Nodes: []FlowNode{{ID: "a", N: "x", T: NodeEnd}}, Edges: nil}
	if bad.Valid() == nil {
		t.Fatal("没有开始节点必须被判为不可执行")
	}
	if (&Flow{Nodes: []FlowNode{{ID: "a", N: "s", T: NodeStart}}, Edges: [][3]string{{"a", "zz", ""}}}).Valid() == nil {
		t.Fatal("悬空连线必须被判为不可执行")
	}
}

func TestAgentGatesRejectIncompleteDefinition(t *testing.T) {
	// 没有工具白名单：即使评测全过也不许发。
	a := &Agent{ID: "x", Ver: "1.0.0", Prompt: "p",
		Stages: []string{"done", "done", "done", "done", "done"}, Eval: [2]int{10, 10}}
	if GatesPass(AgentGates(a)) {
		t.Fatal("没有工具白名单必须拒绝")
	}
	if AgentGates(nil)[0].OK {
		t.Fatal("没有 Agent 定义时四闸必须全红")
	}
}

// 工具白名单闸：编排用了名单外的工具，闸必须红并点名；Detail 不能再写「无越权」。
//
// 这条曾经是「名单非空就绿」，执行器又只认工具名——四闸全绿也能真的读账单。
func TestAgentToolsGateFlagsOutOfListTools(t *testing.T) {
	mk := func(tools []string) *Agent {
		a := &Agent{ID: "x", Ver: "1.0.0", Prompt: "p", Model: "m",
			Stages: []string{"done", "done", "done", "done", "done"},
			Tools:  tools, Eval: [2]int{10, 10},
			Flow: &Flow{
				Nodes: []FlowNode{
					{ID: "n1", N: "开始", T: NodeStart},
					{ID: "n2", N: "读账单", T: NodeTool, Cfg: map[string]any{"tool": "bill.read"}},
					{ID: "n3", N: "结束", T: NodeEnd},
				},
				Edges: [][3]string{{"n1", "n2", ""}, {"n2", "n3", ""}},
			}}
		a.Digest = AgentDigest(*a)
		a.BuiltDigest = a.Digest
		return a
	}

	// 名单里没有 bill.read，但 n2 在用：不越界是不可能的。
	a := mk([]string{"session.read"})
	gs := AgentGates(a)
	tools := gs[1]
	if tools.Key != "agent-tools" {
		t.Fatalf("闸的顺序变了：%+v", gs)
	}
	if tools.OK || tools.Unknown {
		t.Fatalf("名单外的工具必须判红（而不是「判不了」）：%+v", tools)
	}
	if !strings.Contains(tools.Detail, "bill.read") || !strings.Contains(tools.Detail, "n2") {
		t.Fatalf("Detail 要点名工具与节点：%s", tools.Detail)
	}
	if strings.Contains(tools.Detail, "无越权") {
		t.Fatalf("越界时不许出现「无越权」：%s", tools.Detail)
	}
	if GatesPass(gs) {
		t.Fatal("有越界工具时四闸不许全绿")
	}

	// 名单为空是另一种毛病，Detail 要分开写。
	empty := AgentGates(mk(nil))[1]
	if empty.OK || empty.Detail != "没有工具白名单" {
		t.Fatalf("空名单要说「没有工具白名单」：%+v", empty)
	}

	// 名单对得上：照常绿。
	ok := AgentGates(mk([]string{"session.read", "bill.read"}))[1]
	if !ok.OK {
		t.Fatalf("名单对得上时应为绿：%+v", ok)
	}
}

// ToolsOutside 是闸与执行器共用的判据：只认结构化字段，不猜 `pre` 里的文字。
func TestToolsOutside(t *testing.T) {
	f := &Flow{Nodes: []FlowNode{
		{ID: "n1", N: "开始", T: NodeStart},
		{ID: "n2", N: "读账单", T: NodeTool, Cfg: map[string]any{"tool": "bill.read"}},
		{ID: "n3", N: "候选", T: NodeTool, Cfg: map[string]any{"tools": []any{"shell.exec", "bill.read"}}},
		{ID: "n4", N: "分支", T: NodeBranch, Cfg: map[string]any{
			"pre": "不要调用 shell.exec，也不要用 k8s.apply", "conds": []any{[]any{"else", "走"}}}},
		{ID: "n5", N: "结束", T: NodeEnd},
	}}
	got := f.ToolsOutside([]string{"bill.read"})
	byTool := map[string][]string{}
	for _, o := range got {
		for _, n := range o.Nodes {
			byTool[o.Tool] = append(byTool[o.Tool], n.ID)
		}
	}
	if len(byTool["shell.exec"]) != 1 || byTool["shell.exec"][0] != "n3" {
		t.Fatalf("cfg.tools 里的越界工具要算：%v", byTool)
	}
	if _, ok := byTool["bill.read"]; ok {
		t.Fatalf("名单内的工具不该出现：%v", byTool)
	}
	if _, ok := byTool["k8s.apply"]; ok {
		t.Fatalf("不许从 pre 的一句话里猜工具名：%v", byTool)
	}
	if extra := f.ToolsOutside([]string{"bill.read", "shell.exec"}); len(extra) != 0 {
		t.Fatalf("都给了名单就应当没有越界：%+v", extra)
	}
	if got := (&Flow{}).ToolsOutside([]string{"a"}); len(got) != 0 {
		t.Fatalf("空编排不该有越界项：%+v", got)
	}
	if got := (*Flow)(nil).ToolsOutside([]string{"a"}); got != nil {
		t.Fatalf("nil 编排要安全返回：%+v", got)
	}
}

func TestAgentDigestChangesWithDefinition(t *testing.T) {
	a := Agent{Ver: "1.0.0", Model: "m", Prompt: "p", Tools: []string{"repo.read"}}
	d1 := AgentDigest(a)
	a.Prompt = "p2"
	if AgentDigest(a) == d1 {
		t.Fatal("改提示词必须换 digest")
	}
	a.Prompt = "p"
	a.Tools = []string{"repo.read", "shell.exec"}
	if AgentDigest(a) == d1 {
		t.Fatal("改工具白名单必须换 digest")
	}
}

func TestSvcVerifyZeroTotalIsNotPass(t *testing.T) {
	if (SvcVerify{}).OK() {
		t.Fatal("没验过不等于通过")
	}
	if !(SvcVerify{Pass: 22, Total: 22}).OK() {
		t.Fatal("22/22 应通过")
	}
}

func TestHybridGatesBothSidesMustPass(t *testing.T) {
	pbc := []Gate{{Key: "a", OK: true}, {Key: "b", OK: true}, {Key: "c", OK: true}}
	agAgent := &Agent{ID: "x", Ver: "1.0.0", Prompt: "p", Model: "m",
		Stages: []string{"done", "done", "done", "done", "done"},
		Tools:  []string{"t"}, Eval: [2]int{10, 10}}
	agAgent.Digest = AgentDigest(*agAgent)
	agAgent.BuiltDigest = agAgent.Digest
	ag := AgentGates(agAgent)
	if HybridCanRelease(HybridGates(pbc, SvcVerify{Pass: 20, Total: 22}, ag)) {
		t.Fatal("服务侧 20/22 未全过，组合项不许发布")
	}
	if !HybridCanRelease(HybridGates(pbc, SvcVerify{Pass: 22, Total: 22}, ag)) {
		t.Fatal("两侧全过应可发布")
	}
}

// 打 tag 的验收闸口：没有清单、没验完、验的是旧提交，三种都要挡；
// 清单是 HEAD 这一版且全过才放行（#70）。
func TestCheckBlock(t *testing.T) {
	pass := func(id, commit string) Check {
		return Check{ID: id, N: id, ST: CheckPass, Commit: commit}
	}
	cases := []struct {
		name string
		list []Check
		want string
	}{
		{"没有清单", nil, "没有验收清单"},
		{"还是空的", []Check{{ID: "AC-1", ST: CheckTodo, Commit: "1.0.1"}}, "还差"},
		{"有一条不过", []Check{pass("AC-1", "1.0.1"), {ID: "AC-2", ST: CheckFail, Commit: "1.0.1"}}, "还差"},
		{"结论看不懂按没验算", []Check{pass("AC-1", "1.0.1"), {ID: "AC-2", ST: "", Commit: "1.0.1"}}, "还差"},
		{"全过但是旧提交", []Check{pass("AC-1", "1.0.0"), pass("AC-2", "1.0.0")}, "不是 HEAD 1.0.1"},
		{"全过且是 HEAD", []Check{pass("AC-1", "1.0.1"), pass("AC-2", "1.0.1")}, ""},
	}
	for _, c := range cases {
		got := CheckBlock(c.list, "1.0.1")
		if c.want == "" && got != "" {
			t.Fatalf("%s：应放行，实际挡住：%s", c.name, got)
		}
		if c.want != "" {
			if got == "" {
				t.Fatalf("%s：应挡住，实际放行", c.name)
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("%s：拒绝理由应含 %q，实际 %q", c.name, c.want, got)
			}
		}
	}
}
