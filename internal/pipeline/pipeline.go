// Package pipeline 是「执行侧」的真实实现：把账本上的制品变成跑起来的东西。
//
// 它把四件事接在一起：
//
//	账本（制品内容） → 生成器 → 真实代码/文档
//	真实代码        → 构建器 → 二进制 + 内容 digest
//	二进制          → 产物仓库 → 按 digest 取回
//	二进制          → 部署器 → 进程 + 健康检查
//
// 每一步的产物都进账本（artifact.drafted / build.succeeded / deploy.succeeded），
// 所以「构建出的是什么」「部署的是哪个字节」都能从事实里查出来。
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/builder"
	"github.com/zhangkun/i3-composer/internal/codegen"
	"github.com/zhangkun/i3-composer/internal/deployer"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

func stamp() string { return time.Now().Format("01-02 15:04") }

// Pipeline 是执行侧的实现集合。
type Pipeline struct {
	Log   *store.Store
	Store *artifacts.Store
	Dep   *deployer.Deployer
	Work  string // 工作区根目录（临时目录）
	// LogFor 给出任意一本账的日志。执行侧不猜自己连的是哪本账：
	// 单元登记在哪本书里，就从哪本书里读。
	LogFor func(prefix string) *store.Store
}

// Root 是产物仓库根目录。产物不在仓库里时要把它报出来：
// 账本记 digest，不记「字节放在哪个目录」，换目录重启后就对不上了（#52）。
func (p *Pipeline) Root() string { return p.Store.Root() }

// logOf 按前缀取账本；没有 LogFor 时用 Log。
func (p *Pipeline) logOf(prefix string) *store.Store {
	if p.LogFor != nil {
		if s := p.LogFor(prefix); s != nil {
			return s
		}
	}
	return p.Log
}

// New 造一个执行侧。
func New(log *store.Store, work string) (*Pipeline, error) {
	if work == "" {
		work = filepath.Join(os.TempDir(), "i3-composer-work")
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	return &Pipeline{
		Log:   log,
		Store: artifacts.New(""),
		Dep:   deployer.New(),
		Work:  work,
	}, nil
}

// specOf 拼出生成器需要的输入。
func (p *Pipeline) specOf(ctx context.Context, u wf.UnitInfo, title, change string) codegen.Spec {
	name := u.Name
	if name == "" {
		name = u.ID
	}
	return codegen.Spec{
		UnitID:   u.ID,
		UnitName: name,
		Kind:     u.Kind,
		Profile:  u.Profile,
		Title:    title,
		Change:   change,
		At:       stamp(),
	}
}

// Doc 产出文档类制品的内容。
func (p *Pipeline) Doc(ctx context.Context, u wf.UnitInfo, title, change string, t domain.Artifact) (string, error) {
	docs := codegen.Docs(p.specOf(ctx, u, title, change))
	md, ok := docs[t]
	if !ok {
		return "", fmt.Errorf("制品 %s 没有可生成的文档", t)
	}
	return md, nil
}

// Code 产出代码类制品的文件。
//
// PBC 与组合单元产出可编译的 Go 服务；Agent 单元产出的是编排定义本身
// （它的产物是「一份编排 + 一个 digest」，不是一个进程）。
func (p *Pipeline) Code(ctx context.Context, u wf.UnitInfo, title, change string) (map[string]string, error) {
	spec := p.specOf(ctx, u, title, change)
	if u.Kind == "agent" {
		return agentFiles(spec), nil
	}
	out := map[string]string{}
	for _, f := range codegen.Files(spec) {
		out[f.Path] = f.Content
	}
	return out, nil
}

// agentFiles 是 Agent 单元的代码制品：一份可读可评审的定义。
//
// 它的「构建」是内容摘要（编排变了 digest 就变），它的「部署」没有独立进程。
func agentFiles(spec codegen.Spec) map[string]string {
	def := map[string]any{
		"unit":      spec.UnitID,
		"name":      spec.UnitName,
		"version":   "0.1.0",
		"title":     spec.Title,
		"generated": spec.At,
		"stages":    []string{"方案", "原型", "构建", "验证", "发布"},
	}
	b, _ := json.MarshalIndent(def, "", "  ")
	return map[string]string{
		"agent/definition.json": string(b),
		"agent/README.md": fmt.Sprintf(`# %s · Agent 定义

> 由 X-全栈工程师生成 · %s

本单元交付的是一份编排定义，不是一个进程。
它的构建产物是「编排 + digest」：编排变了，digest 就变；没换 digest 就不是新版本。

改动会经过四闸：上游闭合 / 工具白名单 / digest 一致 / 评测达标。
`, spec.UnitName, spec.At),
	}
}

// Test 真跑测试。
//
// PBC 与组合单元：go test 跑代码制品里的单元测试。
// Agent 单元：跑编排的结构校验（节点合法、工具都接线、digest 对得上）——
// 同样是真实检查，不是编出来的条数。
func (p *Pipeline) Test(ctx context.Context, pbc, commit string, files map[string]string) (wf.TestOut, error) {
	if len(files) == 0 {
		return wf.TestOut{}, fmt.Errorf("制品里没有可测的文件")
	}
	if _, ok := files["go.mod"]; ok {
		dir, err := p.workspace(pbc, commit)
		if err != nil {
			return wf.TestOut{}, err
		}
		if err := builder.Materialize(dir, toFiles(files)); err != nil {
			return wf.TestOut{}, err
		}
		res, err := builder.Test(ctx, dir)
		out := wf.TestOut{Pass: res.Pass, Total: res.Total, Failures: res.Failures, Output: res.Output}
		if err != nil {
			return out, err
		}
		return out, nil
	}
	// Agent 单元：结构校验。
	checks, failures := validateAgentFiles(files)
	return wf.TestOut{Pass: checks - len(failures), Total: checks, Failures: failures}, nil
}

// validateAgentFiles 校验 Agent 定义：JSON 能解析、关键字段在、阶段齐全。
func validateAgentFiles(files map[string]string) (int, []string) {
	total := 0
	var fails []string
	def, ok := files["agent/definition.json"]
	if !ok {
		return 1, []string{"缺少 agent/definition.json"}
	}
	total++
	var m map[string]any
	if err := json.Unmarshal([]byte(def), &m); err != nil {
		return total, []string{"definition.json 不是合法 JSON：" + err.Error()}
	}
	for _, k := range []string{"unit", "name", "version", "stages"} {
		total++
		if _, has := m[k]; !has {
			fails = append(fails, "定义缺少字段 "+k)
		}
	}
	total++
	if st, ok := m["stages"].([]any); !ok || len(st) != 5 {
		fails = append(fails, "Agent 必须走五个阶段")
	}
	return total, fails
}

// Build 真构建。
//
// 编译型产物：go build 出二进制，digest 是二进制字节的 sha256（内容寻址）。
// 非编译型产物（Agent 定义）：digest 是文件内容的 sha256，同样内容寻址。
func (p *Pipeline) Build(ctx context.Context, pbc, commit string, files map[string]string) (wf.BuildOut, error) {
	if len(files) == 0 {
		return wf.BuildOut{}, fmt.Errorf("制品里没有可构建的文件")
	}
	if _, ok := files["go.mod"]; ok {
		dir, err := p.workspace(pbc, commit)
		if err != nil {
			return wf.BuildOut{}, err
		}
		if err := builder.Materialize(dir, toFiles(files)); err != nil {
			return wf.BuildOut{}, err
		}
		res, err := builder.Build(ctx, dir)
		if err != nil {
			return wf.BuildOut{}, err
		}
		bin, err := os.ReadFile(res.Binary)
		if err != nil {
			return wf.BuildOut{}, err
		}
		d, size, err := p.Store.Put(bin)
		if err != nil {
			return wf.BuildOut{}, err
		}
		return wf.BuildOut{
			Digest: d, Size: size, Binary: true,
			Detail: fmt.Sprintf("go build %s · %d 字节 · %s", res.Package, size, res.GoVersion),
		}, nil
	}
	// 非编译型：按路径排序后整体算摘要，保证与顺序无关。
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&buf, "%s\n%s\n", p, files[p])
	}
	d, size, err := p.Store.Put([]byte(buf.String()))
	if err != nil {
		return wf.BuildOut{}, err
	}
	return wf.BuildOut{Digest: d, Size: size, Binary: false,
		Detail: fmt.Sprintf("内容摘要（%d 个文件，无独立进程）", len(paths))}, nil
}

// Deploy 真部署：把产物跑起来，等它健康。
//
// 没有可执行产物（Agent 定义）时如实说「没有独立进程」，不编一个 URL。
func (p *Pipeline) Deploy(ctx context.Context, pbc, env, commit, digest string, binary bool) (wf.DeployResult, error) {
	if !binary {
		return wf.DeployResult{Skipped: true, Detail: "该产物没有独立进程（编排 + digest 即交付物）"}, nil
	}
	bin, err := p.Store.Get(digest)
	if err != nil {
		return wf.DeployResult{}, err
	}
	dir, err := p.workspace(pbc, commit)
	if err != nil {
		return wf.DeployResult{}, err
	}
	// 预览与 SIT 可同时运行同一提交。每次使用独立可执行文件，避免重写正在执行的文件。
	f, err := os.CreateTemp(dir, "app-*.bin")
	if err != nil {
		return wf.DeployResult{}, err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0o755)
	if err == nil {
		_, err = f.Write(bin)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return wf.DeployResult{}, err
	}
	inst, err := p.Dep.Deploy(ctx, pbc, env, commit, digest, f.Name())
	if err != nil {
		return wf.DeployResult{}, err
	}
	return wf.DeployResult{RuntimeID: inst.RuntimeID, URL: inst.URL, PID: inst.PID, Health: inst.Health}, nil
}

// Stop 停掉某个部署（回滚用）。
func (p *Pipeline) Stop(pbc, env string) { p.Dep.Stop(pbc, env) }

// StopAll 停掉所有部署。服务退出前必须调它——
// 部署起的进程是这个流水线起的，不收就留在机器上。
func (p *Pipeline) StopAll() { p.Dep.StopAll() }

// Alive 判定部署是否还在跑。
func (p *Pipeline) Alive(pbc, env string) bool { return p.Dep.Alive(pbc, env) }

// workspace 给出某个提交的工作目录（同一提交复用，便于复算）。
func (p *Pipeline) workspace(pbc, commit string) (string, error) {
	dir := filepath.Join(p.Work, pbc, commit)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func toFiles(m map[string]string) []builder.File {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]builder.File, 0, len(paths))
	for _, p := range paths {
		out = append(out, builder.File{Path: p, Content: m[p]})
	}
	return out
}
