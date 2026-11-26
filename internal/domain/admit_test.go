package domain

import "testing"

// 五项全过：硬项齐、软项也有证据 → pass，无 reason 无 waiver。
func TestAdmitAllPass(t *testing.T) {
	in := AdmitInput{
		ID: "US-01", Title: "业主查看并支付账单", Type: "feature", Kind: "Story",
		Accept:   []string{"当业主进入账单页，则应看到本期应缴金额"},
		Data:     []string{"fee.bill.amount"},
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
		HasTaskBreakdown: true,
		HasBacklog:       true,
	}
	r := Admit(in)
	if !r.Pass {
		t.Fatalf("五项齐应放行，实际 reasons=%+v waivers=%v", r.Reasons, r.Waivers)
	}
	if len(r.Reasons) != 0 || len(r.Waivers) != 0 {
		t.Fatalf("全过不该有 reason/waiver：%+v %v", r.Reasons, r.Waivers)
	}
}

// 缺硬项：只有标题 → reject，reason 精确到三项硬项，且不把软项算进 reason。
func TestAdmitRejectsMissingHardItems(t *testing.T) {
	r := Admit(AdmitInput{ID: "US-01", Title: "优化一下", Type: "feature", Kind: "Story"})
	if r.Pass {
		t.Fatal("只有标题绝不该放行")
	}
	got := map[string]bool{}
	for _, c := range r.Reasons {
		got[c.Key] = true
		if !c.Hard {
			t.Fatalf("reason 里只该有硬项，混进了 %s", c.Key)
		}
		if c.Missing == "" || c.Ask == "" || c.Fix == "" {
			t.Fatalf("%s 没过时必须给「缺什么 / 找谁 / 补哪」，实际 %+v", c.Key, c)
		}
	}
	for _, want := range []string{"target", "data", "owner"} {
		if !got[want] {
			t.Fatalf("应点名缺 %s，实际 %v", want, got)
		}
	}
}

// 软项不过：硬项齐 → 放行，但 waiver 里有一条一条的「哪项没补」。
func TestAdmitWaivesSoftItems(t *testing.T) {
	r := Admit(AdmitInput{
		ID: "US-01", Title: "x", Type: "feature", Kind: "Story",
		Accept:   []string{"当提交订单，则返回幂等键"},
		Data:     []string{"fee.bill"},
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
		// 没拆 task，也没 backlog → 两项软项都不过。
	})
	if !r.Pass {
		t.Fatalf("软项不过不该拦，实际 %+v", r.Reasons)
	}
	if len(r.Waivers) != 2 {
		t.Fatalf("应记两条 waiver（范围边界 / 上下文齐备），实际 %v", r.Waivers)
	}
}

// unknown 不算拦阻，也不进 waiver：它是「本系统判不了」，不是「人没补」。
func TestAdmitUnknownDoesNotBlock(t *testing.T) {
	r := Admit(AdmitInput{
		ID: "US-01", Title: "x", Type: "feature", Kind: "Story",
		Accept:   []string{"当提交订单，则返回幂等键"},
		Data:     []string{"fee.bill"}, // 声明了，但权限开没开判不了
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
		HasTaskBreakdown: true, HasBacklog: true,
	})
	if !r.Pass {
		t.Fatal("unknown 不该拦")
	}
	if len(r.Waivers) != 0 {
		t.Fatalf("unknown 不进 waiver，实际 %v", r.Waivers)
	}
	var dataCheck *AdmitCheck
	for i := range r.Checks {
		if r.Checks[i].Key == "data" {
			dataCheck = &r.Checks[i]
		}
	}
	if dataCheck == nil || dataCheck.Verdict != AdmitUnknown {
		t.Fatalf("数据项应为 unknown（权限判不了），实际 %+v", dataCheck)
	}
}

// 生成的模板正文不算人的验收口径；只有手动编辑的 AT 才顶用。
func TestAdmitAcceptsHumanWrittenAT(t *testing.T) {
	base := AdmitInput{
		ID: "US-01", Title: "x", Type: "feature", Kind: "Story",
		Data:     []string{"fee.bill"},
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
		HasTaskBreakdown: true, HasBacklog: true,
	}
	// 只有一条不可断言的「口径」：拦。
	bad := base
	bad.HumanAcceptText = "见 AT 制品：每条 Given/When/Then 都要能被真跑一遍。"
	if r := Admit(bad); r.Pass {
		t.Fatalf("模板正文不该算可断言，实际放行了：%+v", r.Checks)
	}
	// 人写的 Given/When/Then：过。
	ok := base
	ok.HumanAcceptText = "当业主提交缴费单，则应生成幂等键且重复提交不重复入账"
	ok.HumanAcceptFrom = "AT"
	if r := Admit(ok); !r.Pass {
		t.Fatalf("人写的 AT 应能过目标项，实际 %+v", r.Reasons)
	}
}

// 占位名字不算责任人：「—」「内部」不是人。
func TestAdmitPlaceholderOwnerIsNotAPerson(t *testing.T) {
	r := Admit(AdmitInput{
		ID: "US-01", Title: "x", Type: "feature", Kind: "Story",
		Accept:   []string{"当提交订单，则返回幂等键"},
		Data:     []string{"fee.bill"},
		AcceptBy: "—", ComplyBy: "内部",
		HasTaskBreakdown: true, HasBacklog: true,
	})
	if r.Pass {
		t.Fatal("占位名不算责任人，不该放行")
	}
	var owner *AdmitCheck
	for i := range r.Reasons {
		if r.Reasons[i].Key == "owner" {
			owner = &r.Reasons[i]
		}
	}
	if owner == nil {
		t.Fatalf("应点名责任人项，实际 %+v", r.Reasons)
	}
}

// doc / backlog 类型不涉及服务边界，范围项直接过。
func TestAdmitDocScopePasses(t *testing.T) {
	r := Admit(AdmitInput{
		ID: "T-1", Title: "补 BRD", Type: "doc", Kind: "Story",
		Accept:   []string{"当打开 BRD，则应看到 §4 范围与账期规则"},
		Data:     []string{"brd.doc"},
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
	})
	if !r.Pass {
		t.Fatalf("doc 类型应放行，实际 %+v", r.Reasons)
	}
	for _, w := range r.Waivers {
		if len(w) > 0 && containsLabel(w, "范围边界") {
			t.Fatalf("doc 类型的范围项应通过，不该有 waiver：%v", r.Waivers)
		}
	}
}

func containsLabel(s, label string) bool {
	return len(s) >= len(label) && (s[:len(label)] == label)
}
