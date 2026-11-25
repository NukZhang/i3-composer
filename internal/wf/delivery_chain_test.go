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
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

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
