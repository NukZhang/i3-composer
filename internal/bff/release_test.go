package bff

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

// 账本说「这个提交构建过、digest 是 X」，仓库里却没有 X 的字节——
// 比如换 ARTIFACT_DIR 后重启（#52）。发布必须失败，且错误里要能看出
// 三件事：仓库在哪、digest 出自哪条构建事实、字节确实不在。
func TestReleaseNamesDigestSourceWhenBlobMissing(t *testing.T) {
	h := newHarness(t)
	empty := filepath.Join(t.TempDir(), "artifacts")

	// 同一个 sidecar、同一个账本前缀，只把执行侧的仓库换成空目录：
	// 与「重启后换 ARTIFACT_DIR」是同一个形状。
	b := NewServer(h.st, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	pipe, err := pipeline.New(h.st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipe.Store = artifacts.New(empty)
	b.Pipe = pipe

	registerUnit(t, h.st, "rel1")
	ctx := t.Context()
	if _, err := h.st.Append(ctx, "rel1", store.FactCommitCreated, "i3-pipeline", domain.Commit{
		No: "1.0.1", By: "fast", At: "2026-11-27 05:00:00",
	}); err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:00000000000000000000000000000000000000000000000000000000000000aa"
	if _, err := h.st.Append(ctx, "rel1", store.FactBuildSucceeded, "i3-pipeline", map[string]any{
		"commit": "1.0.1", "digest": digest, "binary": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, "rel1", store.FactTagCreated, "SA", map[string]any{
		"tag": "v1.0.0", "commit": "1.0.1",
	}); err != nil {
		t.Fatal(err)
	}

	s := httptest.NewServer(b.Handler())
	t.Cleanup(s.Close)
	resp, out := h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/release", map[string]any{
		"env": "UAT", "mode": "full", "units": []string{"rel1"},
	}, RoleSA)
	if resp.StatusCode == http.StatusCreated {
		t.Fatalf("产物不在仓库里却发布成功：%v", out)
	}
	msg, _ := out["error"].(string)
	for _, want := range []string{
		"不在仓库里",
		"仓库根 " + empty,
		"digest 出自 build.succeeded",
		"seq=",
		"1.0.1",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息应包含 %q，实际：%s", want, msg)
		}
	}
}

func TestReleaseHealthEndpointAllowsOnlyLocalRuntime(t *testing.T) {
	for address, expected := range map[string]string{
		"http://127.0.0.1:32001":       "http://127.0.0.1:32001/healthz",
		"http://[::1]:32001/":          "http://[::1]:32001/healthz",
		"http://192.0.2.1:32001":       "",
		"http://localhost:32001":       "",
		"http://user@127.0.0.1:32001":  "",
		"http://127.0.0.1:32001/admin": "",
		"http://127.0.0.1:32001?a=b":   "",
	} {
		if actual := releaseHealthEndpoint(address); actual != expected {
			t.Errorf("releaseHealthEndpoint(%q) = %q, want %q", address, actual, expected)
		}
	}
}

func TestRecordReleaseBatchAllocatesDistinctIDs(t *testing.T) {
	h := newHarness(t)
	server := NewServer(h.st, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	type result struct {
		id  string
		err error
	}
	out := make(chan result, 8)
	for range cap(out) {
		go func() {
			batch := store.ReleaseBatch{
				Proj: "p1", Env: "SIT", By: "SA", Units: []store.ReleaseUnit{{ID: "qa"}},
			}
			err := server.recordReleaseBatch(t.Context(), &batch)
			out <- result{id: batch.ID, err: err}
		}()
	}
	seen := map[string]bool{}
	for range cap(out) {
		res := <-out
		if res.err != nil || res.id == "" || seen[res.id] {
			t.Errorf("并发发布必须得到不同的批次号，实际 %q，%v", res.id, res.err)
		}
		seen[res.id] = true
	}
}

func TestReleaseRuntimeIgnoresOutOfOrderBatchRecords(t *testing.T) {
	h := newHarness(t)
	pipe, err := pipeline.New(h.st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipe.Store = artifacts.New(t.TempDir())
	t.Cleanup(pipe.StopAll)
	server := NewServer(h.st, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	server.Pipe = pipe
	api := httptest.NewServer(server.Handler())
	t.Cleanup(api.Close)

	ctx := t.Context()
	const unitID, env, commit = "dun", "SIT", "1.0.1"
	files, err := pipe.Code(ctx, wf.UnitInfo{ID: unitID, Kind: "pbc"}, "批次核对", "new")
	if err != nil {
		t.Fatal(err)
	}
	build, err := pipe.Build(ctx, unitID, commit, files)
	if err != nil {
		t.Fatal(err)
	}
	deployBatch := func() store.ReleaseBatch {
		t.Helper()
		out, err := pipe.Deploy(ctx, unitID, env, commit, build.Digest, true)
		if err != nil {
			t.Fatal(err)
		}
		return store.ReleaseBatch{Proj: "p1", Env: env, By: "SA", Status: "active", Units: []store.ReleaseUnit{{
			ID: unitID, Binary: true, Commit: commit, Tag: "v1.0.0", Digest: build.Digest, URL: out.URL, RuntimeID: out.RuntimeID,
		}}}
	}
	old, current := deployBatch(), deployBatch()
	// 复现早启动的请求晚落链。批次的账本顺序不能代替进程身份。
	for _, batch := range []*store.ReleaseBatch{&current, &old} {
		if err := server.recordReleaseBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}

	for _, check := range []struct {
		batchID string
		status  int
	}{{current.ID, http.StatusOK}, {old.ID, http.StatusNotFound}} {
		path := "/api/projects/p1/releases/" + check.batchID + "/units/" + unitID + "/health"
		response, proof := h.doOn(t, api, http.MethodGet, path, nil, RoleSA)
		if response.StatusCode != check.status || (check.status == http.StatusOK && proof["batch"] != current.ID) {
			t.Errorf("批次 %s 核对返回 %d %v，期望状态 %d", check.batchID, response.StatusCode, proof, check.status)
		}
	}

	for _, batch := range []store.ReleaseBatch{old, current} {
		response, body := h.doOn(t, api, http.MethodPost, "/api/projects/p1/orch/rollback", map[string]any{"id": batch.ID}, RoleSA)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("批次 %s 回滚返回 %d %v", batch.ID, response.StatusCode, body)
		}
		if alive := pipe.Alive(unitID, env); alive != (batch.ID == old.ID) {
			t.Errorf("回滚 %s 后进程存活 = %v", batch.ID, alive)
		}
	}
}
