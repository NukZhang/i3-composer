package codegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 生成的代码必须真的能编译、真的能跑过测试。
// 这条断言是整个执行侧的地基：如果生成物编译不过，后面的构建/部署全是空话。
func TestGeneratedCodeCompilesAndTestsPass(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	spec := Spec{
		UnitID:   "fee",
		UnitName: "收费缴费",
		Kind:     "pbc",
		Title:    "批量生成缴费单",
		Change:   "feature",
		At:       "11-24 10:00",
	}
	dir := t.TempDir()
	for _, f := range Files(spec) {
		p := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("test", "-count=1", "./..."); err != nil {
		t.Fatalf("生成的代码应通过测试：%v\n%s", err, out)
	}
	if out, err := run("build", "-o", "app.bin", "."); err != nil {
		t.Fatalf("生成的代码应能编译：%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.bin")); err != nil {
		t.Fatalf("应产出二进制：%v", err)
	}
}

// 文档与代码说的是同一套口径：AT 里的每条 AC 都得在测试里出现。
func TestAcceptanceIsSharedBetweenDocAndTests(t *testing.T) {
	spec := Spec{UnitID: "fee", UnitName: "收费缴费", Kind: "pbc", Title: "t", At: "now"}
	acs := spec.Acceptance()
	if len(acs) != 5 {
		t.Fatalf("应有 5 条骨架验收标准，实际 %d", len(acs))
	}
	at := spec.at()
	tests := Code(spec)["main_test.go"]
	for _, a := range acs {
		if !contains(at, a.ID) {
			t.Fatalf("AT 文档缺 %s", a.ID)
		}
		if !contains(tests, a.ID) {
			t.Fatalf("测试缺 %s 的用例", a.ID)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// 代码里不许出现写死的业务口径或假数据（#93）：费率、封顶、种子记录。
func TestGeneratedCodeHasNoHardcodedBusinessRules(t *testing.T) {
	for _, spec := range []Spec{
		{UnitID: "fee", UnitName: "收费缴费", Kind: "pbc", Title: "批量生成缴费单", At: "11-24 10:00"},
		{UnitID: "wo", UnitName: "运维", Kind: "pbc", Title: "登记报修工单", At: "11-24 10:00"},
		{UnitID: "x1", UnitName: "X", Kind: "pbc", Title: "数据导出", At: "11-24 10:00"},
	} {
		code := Code(spec)["main.go"] + Code(spec)["main_test.go"]
		for _, bad := range []string{"0.0005", "0.20", "滞纳金", "逾期", "A-101", "B-201", "lateFee"} {
			if strings.Contains(code, bad) {
				t.Fatalf("%s 的生成代码里不该出现写死的业务口径 %q", spec.Title, bad)
			}
		}
	}
}

// 术语跟着标题走：同样的骨架，不同标题得到不同的资源名与字段。
func TestTermsFollowTitle(t *testing.T) {
	fee := Spec{UnitID: "fee", Title: "批量生成缴费单"}
	wo := Spec{UnitID: "wo", Title: "登记报修工单"}
	if fee.Terms().Plural != "invoices" || !fee.Terms().Amount {
		t.Fatalf("缴费单应是 invoices 且带金额口径，实际 %+v", fee.Terms())
	}
	if wo.Terms().Plural != "tickets" || wo.Terms().Amount {
		t.Fatalf("工单应是 tickets 且没有金额口径，实际 %+v", wo.Terms())
	}
	if strings.Contains(Code(fee)["main.go"], "/api/tickets") {
		t.Fatal("缴费单的代码不该出现工单的路径")
	}
	if !strings.Contains(Code(wo)["main.go"], "/api/tickets") {
		t.Fatal("工单的代码应当用 /api/tickets")
	}
	// 认不出来的标题退回通用记录，不套任何已知业务。
	other := Spec{UnitID: "exp", Title: "数据导出"}
	if other.Terms().Amount {
		t.Fatalf("认不出来的标题不该假设金额口径，实际 %+v", other.Terms())
	}
}
