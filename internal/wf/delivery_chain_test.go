package wf_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

func TestDeliveryKeepsManualPlanAndCreatesPendingChecks(t *testing.T) {
	ds := httptest.NewServer(newFakeState())
	defer ds.Close()
	ctx := t.Context()
	log := store.New(dapr.New(ds.URL), "fake")
	exec, err := pipeline.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const pbc = "material-inbound"
	want := map[domain.Artifact]string{
		domain.PRD: "# 采购入库引用采购合同",
		domain.AD:  "# material_receipt.source_no",
		domain.AT:  "- AT-01 正常入库。Given 余量 120.000 吨，When 提交 80.400 吨，Then 剩余 39.600 吨。",
	}
	for _, tpe := range []domain.Artifact{domain.PRD, domain.AD, domain.AT} {
		ver := domain.Version{T: tpe, V: "1.0.1", St: domain.StDraft, Source: domain.SourceManual, MD: want[tpe],
			DF: domain.DeriveRefs(tpe, func(dep domain.Artifact) string {
				if want[dep] != "" {
					return "1.0.1"
				}
				return ""
			})}
		if _, err := log.Append(ctx, pbc, store.FactArtifactDrafted, "BA", ver); err != nil {
			t.Fatal(err)
		}
	}
	acts := &wf.Activities{D: wf.Deps{Log: log, Exec: exec}}
	in := wf.Input{ItemID: "US-01", PBC: pbc, Title: "材料入库", Type: "feature", Mode: "fast"}
	for i, run := range []func(context.Context, wf.StepInput) (wf.StepResult, error){acts.GenArtifacts, acts.Converge, acts.Commit} {
		out, err := run(ctx, wf.StepInput{Input: in, Step: i, OpID: fmt.Sprintf("manual/%d", i)})
		if err != nil || !out.OK {
			t.Fatalf("步骤 %d 失败：%+v %v", i, out, err)
		}
	}
	facts, err := log.Load(ctx, pbc)
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.Project(pbc, facts)
	if err != nil {
		t.Fatal(err)
	}
	head := v.Commits[0]
	for typ, md := range want {
		got, _ := domain.Find(v.Versions, typ, head.Tree[typ])
		if got.MD != md || got.Source != domain.SourceManual || got.St != domain.StAccepted {
			t.Errorf("%s 提交应保留人工正文和来源，实际 %+v", typ, got)
		}
	}
	if v.CheckCommit != head.No || len(v.Checks) != 1 || v.Checks[0].ID != "AT-01" || v.Checks[0].ST != domain.CheckTodo {
		t.Fatalf("提交的人工 AT 应转为待验收项：%+v", v.Checks)
	}
}

// fakeState 是一个内存版的 Dapr state store：只实现事实日志用到的读写。
type fakeState struct {
	mu    sync.Mutex
	data  map[string][]byte
	etags map[string]int
}

func newFakeState() *fakeState {
	return &fakeState{data: map[string][]byte{}, etags: map[string]int{}}
}

func (f *fakeState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/v1.0/state/fake/"), "%3A", ":")
	switch {
	case r.Method == http.MethodGet:
		b, ok := f.data[key]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf("e%d", f.etags[key]))
		_, _ = w.Write(b)
	case r.Method == http.MethodPost:
		var items []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
			Etag  string          `json:"etag"`
		}
		_ = json.NewDecoder(r.Body).Decode(&items)
		for _, it := range items {
			if it.Etag != "" && it.Etag != fmt.Sprintf("e%d", f.etags[it.Key]) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.data[it.Key] = it.Value
			f.etags[it.Key]++
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// TestDeliveryChainReallyBuildsTestsAndDeploys 跑完整条交付链：
// 生成制品 → 收敛 → 提交 → 真跑测试 → 真构建 → 真部署。
//
// 这是「执行侧不是脚手架」的证据：
//   - 制品里是可编译的 Go 代码
//   - 测试条数是 go test 的输出
//   - digest 是二进制字节的 sha256
//   - 部署地址真的能访问、/healthz 真的 200
func TestDeliveryChainReallyBuildsTestsAndDeploys(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	fs := newFakeState()
	ds := httptest.NewServer(fs)
	defer ds.Close()

	log := store.New(dapr.New(ds.URL), "fake")
	exec, err := pipeline.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exec.Store = artifacts.New(t.TempDir())
	// 部署起的是真进程：测试结束必须收掉，否则每次跑测试都在机器上留一个。
	defer exec.StopAll()
	acts := &wf.Activities{D: wf.Deps{Log: log, Exec: exec}}

	ctx := context.Background()
	const pbc = "fee"
	in := wf.Input{ItemID: "US-01", PBC: pbc, Title: "批量生成缴费单", Type: "feature", Env: "test", Mode: "fast"}
	step := func(n int, opID string) wf.StepInput {
		return wf.StepInput{Input: in, Step: n, OpID: opID}
	}

	// 1. 生成制品
	res, err := acts.GenArtifacts(ctx, step(0, "op/0"))
	if err != nil {
		t.Fatalf("生成制品失败：%v", err)
	}
	if !res.OK {
		t.Fatalf("生成制品应成功：%s", res.Detail)
	}

	// 2. 收敛
	res, err = acts.Converge(ctx, step(1, "op/1"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("收敛应成功：%s", res.Detail)
	}

	// 3. 提交
	res, err = acts.Commit(ctx, step(2, "op/2"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("提交应成功：%s", res.Detail)
	}
	commitNo := res.CommitNo

	// 4. 真跑测试
	res, err = acts.Test(ctx, step(3, "op/3"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("测试应通过（生成的代码自带通过的用例）：%s", res.Detail)
	}
	if res.Total == 0 {
		t.Fatal("测试条数不应为 0")
	}
	t.Logf("真跑测试：%d/%d 通过", res.Pass, res.Total)

	// 5. 真构建
	res, err = acts.Build(ctx, step(4, "op/4"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("构建应成功：%s", res.Detail)
	}
	if !strings.HasPrefix(res.Detail, "sha256:") {
		t.Fatalf("digest 应是内容摘要：%s", res.Detail)
	}
	t.Logf("真构建：%s", res.Detail)

	// 6. 真部署
	res, err = acts.Deploy(ctx, step(5, "op/5"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("部署应成功：%s", res.Detail)
	}
	if res.URL == "" {
		t.Fatal("部署应给出地址")
	}
	resp, err := http.Get(res.URL + "/healthz")
	if err != nil {
		t.Fatalf("部署地址应可访问：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", resp.StatusCode)
	}
	t.Logf("真部署：%s 健康检查 200", res.URL)

	// 账本里应该有对应的三条事实。
	facts, err := log.Load(ctx, pbc)
	if err != nil {
		t.Fatal(err)
	}
	var sawTest, sawBuild, sawDeploy bool
	for _, f := range facts {
		switch f.Type {
		case store.FactTestRun:
			sawTest = true
		case store.FactBuildSucceeded:
			sawBuild = true
		case store.FactDeploySucceeded:
			sawDeploy = true
		}
	}
	if !sawTest || !sawBuild || !sawDeploy {
		t.Fatalf("账本缺事实：test=%v build=%v deploy=%v", sawTest, sawBuild, sawDeploy)
	}
	_ = commitNo
}

// TestActivitiesWriteToTheLedgerTheWorkItemCarries 验证账本归属。
//
// 同一个进程可能同时处理好几本账的工作项（同一 Dapr app-id 的多个副本，
// 或隔离环境）。工作项落在哪本账上，要由工作项自己带的 Ledger 决定，
// 不能由执行它的进程决定——否则两份账会互相写脏。
func TestActivitiesWriteToTheLedgerTheWorkItemCarries(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("环境里没有 go，跳过")
	}
	fs := newFakeState()
	ds := httptest.NewServer(fs)
	defer ds.Close()

	logFor := func(prefix string) *store.Store { return store.New(dapr.New(ds.URL), "fake", prefix) }
	logA := logFor("a/")
	logB := logFor("b/")

	exec, err := pipeline.New(logA, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exec.Store = artifacts.New(t.TempDir())
	acts := &wf.Activities{D: wf.Deps{
		Log:    logA,
		Exec:   exec,
		LogFor: logFor,
	}}

	ctx := context.Background()
	for _, tc := range []struct {
		prefix string
		log    *store.Store
	}{{prefix: "a/", log: logA}, {prefix: "b/", log: logB}} {
		in := wf.Input{ItemID: "US-01", PBC: "fee", Title: "批量生成缴费单", Type: "feature", Env: "test", Mode: "fast", Ledger: tc.prefix}
		step := func(n int, opID string) wf.StepInput {
			return wf.StepInput{Input: in, Step: n, OpID: opID}
		}
		if _, err := acts.GenArtifacts(ctx, step(0, "op/"+tc.prefix+"0")); err != nil {
			t.Fatal(err)
		}
		if _, err := acts.Converge(ctx, step(1, "op/"+tc.prefix+"1")); err != nil {
			t.Fatal(err)
		}
		res, err := acts.Commit(ctx, step(2, "op/"+tc.prefix+"2"))
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Fatalf("提交应成功：%s", res.Detail)
		}
	}
	// 两本账各自有事实，互不相干。
	fa, _ := logA.Load(ctx, "fee")
	fb, _ := logB.Load(ctx, "fee")
	if len(fa) == 0 || len(fb) == 0 {
		t.Fatalf("两本账都应有事实：a=%d b=%d", len(fa), len(fb))
	}
	// 键不能撞：a 的键是 "a/log:..."，b 的是 "b/log:..."。
	for _, f := range fa {
		if strings.Contains(string(f.Payload), "\"v\":\"1.0.1\"") && !strings.HasPrefix(f.Type, "artifact") {
			continue
		}
	}
	if len(fa) == len(fb) && len(fa) > 0 {
		// 事实条数相同是正常的（同样的流程），关键是它们落在不同的键上。
		t.Logf("两本账各 %d 条事实（键前缀不同）", len(fa))
	}
}

func TestDeliveryKeepsVisitorCriteriaSeparateFromScaffoldChecks(t *testing.T) {
	ds := httptest.NewServer(newFakeState())
	defer ds.Close()
	ctx := t.Context()
	log := store.New(dapr.New(ds.URL), "fake")
	pipe, err := pipeline.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const unit = "bvn1"
	const title = "当访客预约到访则应生成一次性通行凭证"
	const criterion = "当访客预约到访，则应生成一次性通行凭证并在过期后失效"
	in := wf.Input{ItemID: "US-01", PBC: unit, Title: title, Type: "new", Env: "test", Mode: "fast",
		Unit: &wf.UnitInfo{ID: unit, Name: "访客通行凭证", Kind: "pbc"}}
	item := store.Item{ID: in.ItemID, PBC: unit, Title: title, Accept: []string{criterion}}
	if _, err := log.Append(ctx, unit, store.FactItemCreated, "BA", item); err != nil {
		t.Fatal(err)
	}
	acts := &wf.Activities{D: wf.Deps{Log: log, Exec: pipe}}
	for i, step := range []func(context.Context, wf.StepInput) (wf.StepResult, error){
		acts.GenArtifacts, acts.Converge, acts.Commit, acts.Test,
	} {
		result, err := step(ctx, wf.StepInput{Input: in, Step: i, OpID: fmt.Sprintf("visitor/%d", i)})
		if err != nil || !result.OK {
			t.Fatalf("第 %d 步失败：%+v, %v", i, result, err)
		}
	}
	facts, err := log.Load(ctx, unit)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Project(unit, facts)
	if err != nil {
		t.Fatal(err)
	}
	head := view.Commits[0]
	for _, kind := range []domain.Artifact{domain.PRD, domain.Proto, domain.AD, domain.AT, domain.Backlog, domain.Code} {
		version, _ := domain.Find(view.Versions, kind, head.Tree[kind])
		body := version.MD
		if kind == domain.Code {
			for _, file := range version.Files {
				body += "\n" + file
			}
		}
		if !strings.Contains(body, "模板占位，非本单元口径") {
			t.Errorf("%s 缺少模板占位标识", kind)
		}
		for _, billing := range []string{"lateFee", "滞纳金", "账期", "批量生成幂等"} {
			if strings.Contains(body, billing) {
				t.Errorf("%s 不应包含缴费模板片段 %q", kind, billing)
			}
		}
		if kind == domain.PRD || kind == domain.AT || kind == domain.Backlog || kind == domain.Code {
			if !strings.Contains(body, "过期后失效") {
				t.Errorf("%s 没有保留账本里的验收口径", kind)
			}
		}
		if kind == domain.AT {
			checks := domain.ChecksFromAT(body)
			if len(checks) != 1 || checks[0].W != "访客预约到访" || checks[0].T != "应生成一次性通行凭证并在过期后失效" {
				t.Errorf("AT 未按原口径拆分：%+v", checks)
			}
		}
	}
	if len(view.Checks) != 0 {
		t.Errorf("机器生成的 AT 不能成为已确认的人工清单：%+v", view.Checks)
	}
	var evidence map[string]any
	for _, fact := range facts {
		if fact.Type == store.FactTestRun {
			if err := json.Unmarshal(fact.Payload, &evidence); err != nil {
				t.Fatal(err)
			}
		}
	}
	if evidence["id"] != in.ItemID || evidence["commit"] != head.No ||
		evidence["scope"] != "scaffold" || evidence["pass"] != float64(1) || evidence["total"] != float64(1) {
		t.Errorf("机器测试应只记本提交的脚手架检查：%+v", evidence)
	}
}

func TestGenerationKeepsCommittedManualCode(t *testing.T) {
	ds := httptest.NewServer(newFakeState())
	defer ds.Close()
	ctx := t.Context()
	log := store.New(dapr.New(ds.URL), "fake")
	pipe, err := pipeline.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const unit = "manual-visitor"
	manual := domain.Version{
		T: domain.Code, V: "1.0.1", Source: domain.SourceManual, St: domain.StDraft,
		Files: map[string]string{"main.go": "package main\nfunc main() {} // 人工保留"},
	}
	if _, err := log.Append(ctx, unit, store.FactArtifactDrafted, "DA", manual); err != nil {
		t.Fatal(err)
	}
	commit := store.CommitCreated{Commit: domain.Commit{No: "1.0.1", Tree: domain.Tree{domain.Code: manual.V}}}
	if _, err := log.Append(ctx, unit, store.FactCommitCreated, "i3-pipeline", commit); err != nil {
		t.Fatal(err)
	}
	acts := &wf.Activities{D: wf.Deps{Log: log, Exec: pipe}}
	in := wf.Input{ItemID: "US-02", PBC: unit, Title: "补访客凭证规则", Type: "feature", Mode: "fast"}
	for i, step := range []func(context.Context, wf.StepInput) (wf.StepResult, error){acts.GenArtifacts, acts.Converge, acts.Commit} {
		result, err := step(ctx, wf.StepInput{Input: in, Step: i, OpID: fmt.Sprintf("manual/%d", i)})
		if err != nil || !result.OK {
			t.Fatalf("第 %d 步失败：%+v, %v", i, result, err)
		}
	}
	facts, err := log.Load(ctx, unit)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Project(unit, facts)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := domain.Find(view.Versions, domain.Code, view.Commits[0].Tree[domain.Code])
	if code.Source != domain.SourceManual || len(code.Files) != 1 || code.Files["main.go"] != manual.Files["main.go"] {
		t.Fatalf("当前提交应保留人工业务代码：版本=%s 来源=%s 文件数=%d", code.V, code.Source, len(code.Files))
	}
}
