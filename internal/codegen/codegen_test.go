package codegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// 生成的代码必须真的能编译、真的能跑过测试。
// 这条断言是整个执行侧的地基：如果生成物编译不过，后面的构建/部署全是空话。
func TestGeneratedCodeCompilesAndTestsPass(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	for _, spec := range []Spec{
		{UnitID: "fee", UnitName: "收费缴费", Kind: "pbc", Title: "批量生成缴费单", Change: "feature", At: "11-24 10:00", DemoBilling: true},
		{UnitID: "inbound-regression", UnitName: "材料入库", Kind: "pbc", Profile: domain.ProfileMaterialInbound},
	} {
		t.Run(spec.UnitID, func(t *testing.T) { assertGeneratedCode(t, spec) })
	}
}

func assertGeneratedCode(t *testing.T, spec Spec) {
	t.Helper()
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
	spec := Spec{UnitID: "fee", UnitName: "收费缴费", Kind: "pbc", Title: "t", At: "now", DemoBilling: true}
	acs := spec.Acceptance()
	if len(acs) != 5 {
		t.Fatalf("应有 5 条验收标准，实际 %d", len(acs))
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
