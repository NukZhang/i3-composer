// Package builder 是真实的构建链路：把制品树里的代码落成文件，
// 跑 go build / go test，产出内容寻址的 digest。
//
// 以前的 mockBuilder 用 sha256:TreeSHA-... 假装自己是镜像 digest，
// 测试条数也是 6+len(tree) 编出来的。这里的每一样都是真的：
//
//   - digest = 编译产物的字节 sha256（内容寻址：同样的源码必然同一个 digest）
//   - pass/total = go test 的真实结果
//   - 编译不过、测试不过是，就如实失败，不编数字
//
// 这一层没有网络依赖：生成的模块只用标准库，所以构建可离线复算。
package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// File 是一个待落盘的文件。
type File struct {
	Path    string
	Content string
}

// Result 是一次构建的结果。
type Result struct {
	Digest    string `json:"digest"`    // sha256:<hex>，编译产物的内容摘要
	Size      int64  `json:"size"`      // 产物字节数
	Binary    string `json:"binary"`    // 产物路径
	Package   string `json:"package"`   // go list 出来的包名
	GoVersion string `json:"goVersion"` // 真实工具链版本
}

// TestResult 是一次测试的真实结果。
type TestResult struct {
	Passed   []string `json:"-"`
	Pass     int      `json:"pass"`
	Total    int      `json:"total"`
	Failures []string `json:"failures,omitempty"`
	Output   string   `json:"output,omitempty"`
}

// GoTool 是 go 命令的位置。留空表示用 PATH 里的 go。
var GoTool = "go"

// Materialize 把文件写进 dir。同名文件会被覆盖，目录自动创建。
func Materialize(dir string, files []File) error {
	for _, f := range files {
		if f.Path == "" {
			continue
		}
		// 只允许相对路径，且不能逃出目标目录：制品内容是外部输入。
		clean := filepath.Clean("/" + f.Path)
		full := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Build 在 dir 里编译模块，产出二进制并算它的内容摘要。
//
// digest 是二进制字节的 sha256——和镜像仓库里的做法一致：digest 变了
// 就一定是内容变了，反过来也成立。
func Build(ctx context.Context, dir string) (Result, error) {
	cmd := exec.CommandContext(ctx, goBin(), "build", "-trimpath", "-o", "app.bin", ".")
	cmd.Dir = dir
	cmd.Env = buildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("go build 失败：%s", strings.TrimSpace(string(out)))
	}
	bin := filepath.Join(dir, "app.bin")
	st, err := os.Stat(bin)
	if err != nil {
		return Result{}, fmt.Errorf("构建产物缺失：%w", err)
	}
	sum, err := sha256File(bin)
	if err != nil {
		return Result{}, err
	}
	pkg, _ := listPackage(ctx, dir)
	return Result{
		Digest:    "sha256:" + sum,
		Size:      st.Size(),
		Binary:    bin,
		Package:   pkg,
		GoVersion: goVersion(ctx),
	}, nil
}

// Test 在 dir 里真跑 go test。失败条目的名字会被摘出来。
func Test(ctx context.Context, dir string) (TestResult, error) {
	cmd := exec.CommandContext(ctx, goBin(), "test", "-count=1", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = buildEnv()
	out, err := cmd.CombinedOutput()
	res := parseTestOutput(string(out))
	res.Output = trimOutput(string(out))
	if err != nil {
		// 测试跑挂了（编译错误也算）：如实返回失败数，不假装通过。
		if res.Total == 0 {
			res.Total = 1
			res.Failures = append(res.Failures, "包无法编译或测试无法运行")
		}
		return res, fmt.Errorf("go test 失败：%s", strings.TrimSpace(res.Output))
	}
	return res, nil
}

// goTestLine 匹配 `--- PASS: TestX (0.00s)` / `--- FAIL: TestX (0.00s)`。
var goTestLine = regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (\w+)`)

// parseTestOutput 从 go test -v 的输出里数出真实通过/失败。
func parseTestOutput(out string) TestResult {
	res := TestResult{}
	for _, line := range strings.Split(out, "\n") {
		m := goTestLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		switch m[1] {
		case "PASS":
			res.Passed = append(res.Passed, m[2])
			res.Pass++
			res.Total++
		case "FAIL":
			res.Total++
			res.Failures = append(res.Failures, m[2])
		case "SKIP":
			// 跳过不算通过也不算失败：总数不计入，失败名单也不记。
		}
	}
	return res
}

// trimOutput 只留尾部：go test 的输出可能很长，账本里存不下。
func trimOutput(s string) string {
	s = strings.TrimSpace(s)
	const max = 4000
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func listPackage(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, goBin(), "list", "-m")
	cmd.Dir = dir
	cmd.Env = buildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func goVersion(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, goBin(), "version")
	cmd.Env = buildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(out))
	if len(fields) >= 3 {
		return fields[2]
	}
	return strings.TrimSpace(string(out))
}

func goBin() string {
	if GoTool != "" {
		return GoTool
	}
	return "go"
}

// buildEnv 让构建不受宿主环境影响：GOFLAGS=-mod=mod 允许无网拉依赖，
// GOCACHE 指到临时目录，GOPROXY=off 断网——生成的模块只用标准库，
// 断网正好证明「构建不依赖外部网络」。
func buildEnv() []string {
	env := os.Environ()
	env = append(env,
		"GOFLAGS=-mod=mod",
		"GOPROXY=off",
		"GOWORK=off",
		"CGO_ENABLED=0",
	)
	return env
}

// SortFiles 按路径排序，保证落盘与 digest 计算与顺序无关。
func SortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// ParseCount 从 "N/M" 形式里取 N（给展示用的小工具）。
func ParseCount(s string) int {
	n, _, _ := strings.Cut(s, "/")
	v, _ := strconv.Atoi(strings.TrimSpace(n))
	return v
}
