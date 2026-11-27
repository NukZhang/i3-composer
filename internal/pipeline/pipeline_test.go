package pipeline

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/codegen"
)

// newTestStore 造一个落在临时目录里的产物仓库。
func newTestStore(t *testing.T) *artifacts.Store {
	t.Helper()
	return artifacts.New(t.TempDir())
}

// newTestPipeline 造一条执行流水线：工作区与产物仓库都在临时目录里。
//
// 产物仓库必须显式指定：默认是仓库根下的 ./.i3-artifacts，
// 测试跑在包目录下时会在源码树里留下产物。
func newTestPipeline(t *testing.T) *Pipeline {
	t.Helper()
	p, err := New(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.Store = newTestStore(t)
	// 测试起的是真进程：哪怕测试中途失败，也得把它收掉。
	t.Cleanup(p.StopAll)
	return p
}

// testSpec 是测试用的交付单元输入。
func testSpec() codegen.Spec {
	return codegen.Spec{
		UnitID: "fee", UnitName: "收费缴费", Kind: "pbc",
		Title: "测试基线", Change: "new", At: "11-24 10:00",
	}
}

func filesOf(s codegen.Spec) []codegen.File { return codegen.Files(s) }

// 全链路：生成 → 构建 → 产物仓库 → 部署 → 健康检查 → 停止。
//
// 这条测试是「执行侧真的能跑」的证据：它真的编译了代码、真的起了进程、
// 真的等到 /healthz 返回 200。它需要 go 工具链；没有就跳过。
func TestRealBuildAndDeploy(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	ctx := context.Background()
	p := newTestPipeline(t)

	spec := testSpec()
	files := map[string]string{}
	for _, f := range filesOf(spec) {
		files[f.Path] = f.Content
	}

	// 构建：必须是真编译，且 digest 是二进制字节的摘要。
	out, err := p.Build(ctx, "fee", "1.0.1", files)
	if err != nil {
		t.Fatalf("构建应成功：%v", err)
	}
	if !out.Binary {
		t.Fatal("含 go.mod 的制品应产出可执行文件")
	}
	if !strings.HasPrefix(out.Digest, "sha256:") || len(out.Digest) != len("sha256:")+64 {
		t.Fatalf("digest 格式不对：%s", out.Digest)
	}
	// 同样的源码必须得到同样的 digest：内容寻址。
	out2, err := p.Build(ctx, "fee", "1.0.1", files)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Digest != out.Digest {
		t.Fatalf("同样内容的 digest 应相同：%s vs %s", out.Digest, out2.Digest)
	}

	// 产物仓库：按 digest 能取回，且内容校验通过。
	got, err := p.Store.Get(out.Digest)
	if err != nil {
		t.Fatalf("应能按 digest 取回产物：%v", err)
	}
	if len(got) == 0 {
		t.Fatal("取回的产物为空")
	}
	if _, err := p.Store.Get("sha256:00000000000000"); err == nil {
		t.Fatal("不存在的 digest 应报错")
	}

	// 部署：真起进程，等健康检查真的 200。
	dep, err := p.Deploy(ctx, "fee", "test", "1.0.1", out.Digest, true)
	if err != nil {
		t.Fatalf("部署应成功：%v", err)
	}
	if dep.Skipped {
		t.Fatal("有可执行产物时不应跳过部署")
	}
	if dep.Health == "" {
		t.Fatal("部署结果应带健康检查结论")
	}
	resp, err := http.Get(dep.URL + "/healthz")
	if err != nil {
		t.Fatalf("部署地址应可访问：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", resp.StatusCode)
	}
	if !p.Alive("fee", "test") {
		t.Fatal("部署进程应还活着")
	}

	// 停止：回滚走这条路，进程要真的结束。
	p.Stop("fee", "test")
	if p.Alive("fee", "test") {
		t.Fatal("停止后不应还活着")
	}
}

// Agent 单元的代码制品是编排定义：没有独立进程，部署要如实说「跳过」。
func TestAgentArtifactHasNoProcess(t *testing.T) {
	ctx := context.Background()
	p := newTestPipeline(t)
	spec := testSpec()
	spec.Kind = "agent"
	code := map[string]string{}
	for _, f := range filesOf(spec) {
		code[f.Path] = f.Content
	}
	// Agent 单元的代码制品走的是 agentFiles 分支，这里直接构造一份。
	code = map[string]string{
		"agent/definition.json": `{"unit":"qa","name":"交付质检 Agent","version":"1.0.0","stages":["方案","原型","构建","验证","发布"]}`,
		"agent/README.md":       "# 定义\n",
	}
	res, err := p.Test(ctx, "qa", "1.0.1", code)
	if err != nil {
		t.Fatalf("Agent 制品的结构校验应通过：%v", err)
	}
	if res.Total == 0 || res.Pass != res.Total {
		t.Fatalf("结构校验应全过：%+v", res)
	}
	out, err := p.Build(ctx, "qa", "1.0.1", code)
	if err != nil {
		t.Fatalf("Agent 制品应能构建（内容摘要）：%v", err)
	}
	if out.Binary {
		t.Fatal("Agent 定义没有独立进程")
	}
	dep, err := p.Deploy(ctx, "qa", "test", "1.0.1", out.Digest, false)
	if err != nil {
		t.Fatal(err)
	}
	if !dep.Skipped || dep.URL != "" {
		t.Fatalf("没有进程就不该编 URL：%+v", dep)
	}
}

// 坏掉的制品必须让测试失败，而不是被当成全绿。
func TestBrokenCodeFailsTheTest(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	ctx := context.Background()
	p := newTestPipeline(t)
	files := map[string]string{
		"go.mod":       "module i3/broken\n\ngo 1.21\n",
		"main.go":      "package main\n\nfunc main() { this is not go }\n",
		"main_test.go": "package main\n",
	}
	if _, err := p.Test(ctx, "broken", "1.0.1", files); err == nil {
		t.Fatal("编译不过的代码必须让测试失败")
	}
}

// 同一单元推到两个环境：两个进程必须同时活着（#19）。
//
// 根因是所有环境共用一个 app.bin，第二个环境的 WriteFile 覆盖了第一个环境
// 正在执行的可执行文件，那个进程当场退出。这里按真实路径起两个进程来验。
func TestSameUnitTwoEnvsStayAlive(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	ctx := context.Background()
	p := newTestPipeline(t)

	spec := testSpec()
	files := map[string]string{}
	for _, f := range filesOf(spec) {
		files[f.Path] = f.Content
	}
	out, err := p.Build(ctx, "fee", "1.0.1", files)
	if err != nil {
		t.Fatalf("构建应成功：%v", err)
	}
	a, err := p.Deploy(ctx, "fee", "SIT", "1.0.1", out.Digest, true)
	if err != nil {
		t.Fatalf("部署 SIT 应成功：%v", err)
	}
	b, err := p.Deploy(ctx, "fee", "UAT", "1.0.1", out.Digest, true)
	if err != nil {
		t.Fatalf("部署 UAT 应成功：%v", err)
	}
	if a.URL == b.URL {
		t.Fatalf("两个环境应是两个进程，URL 却相同：%s", a.URL)
	}
	if !p.Alive("fee", "SIT") {
		t.Fatal("部署 UAT 之后 SIT 的进程不应被带走")
	}
	if !p.Alive("fee", "UAT") {
		t.Fatal("UAT 的进程应活着")
	}
	// 两个地址都要真的能应答：只看登记表不够，进程可能已经死了而哨兵还没清。
	for _, u := range []string{a.URL, b.URL} {
		resp, err := http.Get(u + "/healthz")
		if err != nil {
			t.Fatalf("%s 应可访问：%v", u, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 健康检查应 200，实际 %d", u, resp.StatusCode)
		}
	}
	// 停一个环境不连坐另一个：回滚只收回自己那一份。
	p.Stop("fee", "SIT")
	if p.Alive("fee", "SIT") {
		t.Fatal("停掉 SIT 之后不应还活着")
	}
	if !p.Alive("fee", "UAT") {
		t.Fatal("停掉 SIT 不应把 UAT 的进程一起收掉")
	}
}
