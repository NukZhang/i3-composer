package pipeline

import (
	"path/filepath"
	"testing"
)

// 同一份产物推到两个环境，可执行文件必须各有一份（#19）。
//
// 以前所有环境共用一个 app.bin：第二个环境一写，第一个环境正在执行的那个就没了。
func TestBinPathPerEnv(t *testing.T) {
	p := &Pipeline{Work: t.TempDir()}
	dg := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sit, err := p.binPath("fee", "SIT", "1.0.1", dg)
	if err != nil {
		t.Fatal(err)
	}
	uat, err := p.binPath("fee", "UAT", "1.0.1", dg)
	if err != nil {
		t.Fatal(err)
	}
	if sit == uat {
		t.Fatalf("两个环境不能共用一个可执行文件：%s", sit)
	}
	// 换个提交也不能撞上：文件名里要有摘要，不只是环境。
	other, _ := p.binPath("fee", "SIT", "1.0.2", "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if other == sit {
		t.Fatalf("不同摘要不能共用一个可执行文件：%s", sit)
	}
	// 环境名里带路径分隔符不能逃出工作目录（「预发/01」这类名字经手会进到文件系统）。
	odd, err := p.binPath("fee", "预发/01", "1.0.1", dg)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(odd) != filepath.Dir(sit) {
		t.Fatalf("环境名不能改变可执行文件所在目录：%s vs %s", odd, sit)
	}
}
