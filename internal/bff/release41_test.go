package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 中间失败的多单元批次（#41）：账本不能留下「success、无批次引用、指向死进程」的部署事实。
//
// 以前是先部署、全部成功后才写批次：第二个单元失败时，第一个单元的 deploy.succeeded 已经落账，
// 没有任何批次引用它，进程又被收回。现在先写批次，失败逐条补 deploy.rolledback + release.batch.failed。
func TestReleaseFailureLeavesNoDanglingDeploy(t *testing.T) {
	h := newHarness(t)
	// 第二个单元没有产物可部署：Digest 指向仓库里不存在的字节。
	registerUnit(t, h.st, "r1")
	registerUnit(t, h.st, "r2")
	ctx := t.Context()
	for _, id := range []string{"r1", "r2"} {
		if _, err := h.st.Append(ctx, id, store.FactCommitCreated, "i3-pipeline", domain.Commit{
			No: "1.0.1", By: "fast", At: "2026-11-27 05:00:00",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.st.Append(ctx, id, store.FactTagCreated, "SA", map[string]any{
			"tag": "v1.0.0", "commit": "1.0.1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 产物用一个真能起起来的小脚本：报出监听地址、等到被停为止（stub 进程就是「真进程」）。
	dir := t.TempDir()
	// 不按固定地址端口起——测试并行时 9101 早被占了；用 python 拿一个空端口再报出来。
	bin := []byte("#!/usr/bin/env python3\n" +
		"import sys, time\n" +
		"from http.server import HTTPServer, BaseHTTPRequestHandler\n" +
		"class H(BaseHTTPRequestHandler):\n" +
		"    def do_GET(self):\n" +
		"        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')\n" +
		"    def log_message(self, *a): pass\n" +
		"srv = HTTPServer(('127.0.0.1', 0), H)\n" +
		"print('listen=127.0.0.1:%d' % srv.server_address[1], file=sys.stderr, flush=True)\n" +
		"srv.serve_forever()\n")
	art := artifacts.New(dir)
	digest, _, err := art.Put(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, "r1", store.FactBuildSucceeded, "i3-pipeline", map[string]any{
		"commit": "1.0.1", "digest": digest, "binary": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, "r2", store.FactBuildSucceeded, "i3-pipeline", map[string]any{
		"commit": "1.0.1",
		"digest": "sha256:00000000000000000000000000000000000000000000000000000000000000bb",
		"binary": true,
	}); err != nil {
		t.Fatal(err)
	}

	b := NewServer(h.st, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	pipe, err := pipeline.New(h.st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipe.Store = art
	b.Pipe = pipe
	s := httptest.NewServer(b.Handler())
	t.Cleanup(s.Close)
	t.Cleanup(pipe.StopAll) // 部署起的进程归测试收

	resp, out := h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/release", map[string]any{
		"env": "UAT", "mode": "full", "units": []string{"r1", "r2"},
	}, RoleSA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("第二个单元没产物却发布成功：%d %v", resp.StatusCode, out)
	}
	msg, _ := out["error"].(string)
	// 失败要说清楚是哪个批次、哪个单元：以前只回一句部署失败，批次号根本没有。
	if !containsAll(msg, "批次 REL-", "r2") {
		t.Fatalf("失败原因应点名批次与单元：%s", msg)
	}

	// 单元账本：r1 的部署事实被撤销，读出来不是 success。
	st := getJSON(t, h, s, "/api/state?pbc=r1")
	deps, _ := st["deploys"].([]any)
	if len(deps) != 1 {
		t.Fatalf("r1 应有一条部署事实（随后被撤销），实际 %d 条：%v", len(deps), deps)
	}
	for _, d := range deps {
		if m, _ := d.(map[string]any); m["status"] == "success" {
			t.Fatalf("失败的批次留下了 success 的部署事实：%v", m)
		}
	}

	// 批次留着，状态 failed，带原因。
	rv, err := b.loadReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.Batches) != 1 {
		t.Fatalf("失败的批次也应留在账上，实际 %d 个：%v", len(rv.Batches), rv.Batches)
	}
	if bm := rv.Batches[0]; bm.Status != "failed" || bm.Reason == "" {
		t.Fatalf("批次应为 failed 且带原因：%+v", bm)
	}
	if id := rv.Batches[0].ID; id == "" {
		t.Fatal("批次号不能为空")
	}
}

// 失败批次留在账上之后，下一个批次的号不能被编成同一个（回滚时捞到哪个就说不清了）。
func TestBatchIDsStayUniqueAfterFailure(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	b := NewServer(h.st, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	if err := h.st.Ensure(ctx, releasesLog); err != nil {
		t.Fatal(err)
	}
	id1 := b.nextBatchID(ctx, "p1")
	if _, err := h.st.Append(ctx, releasesLog, store.FactReleaseBatch, "SA", store.ReleaseBatch{
		ID: id1, Proj: "p1", Status: "active", Units: []store.ReleaseUnit{{ID: "a"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, releasesLog, store.FactReleaseFailed, "SA", map[string]any{"id": id1, "reason": "x"}); err != nil {
		t.Fatal(err)
	}
	if id2 := b.nextBatchID(ctx, "p1"); id2 == id1 {
		t.Fatalf("失败批次占了号之后又编了同一个号：%s", id2)
	}
}

// 主账本与隔离账本各写各的（#41 自测时在 LEDGER_PREFIX 实例上发现的问题）：
// 服务跑在隔离账本上，但发布批次是公共资产、落在主账本上。以前读批次也从隔离账本读，
// 结果是「刚发的批次回滚时 404」，回滚根本没执行，进程留在机器上——而接口还回了 200。
//
// 这里让服务用隔离账本，发布 → 回滚，断言：批次在主账本、单元在隔离账本、
// 单元账本上那条部署事实被撤销、部署进程真的停了。
func TestReleaseOnPrefixedLedgerStillRollsBack(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	iso := store.New(dapr.New(h.ds.URL), "fake", "iso41")
	if err := iso.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "p1x", "name": "p1x", "kind": "pbc", "proj": "p1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p1x", store.FactCommitCreated, "i3-pipeline", domain.Commit{
		No: "1.0.1", By: "fast", At: "2026-11-27 05:00:00",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p1x", store.FactTagCreated, "SA", map[string]any{
		"tag": "v1.0.0", "commit": "1.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := []byte("#!/usr/bin/env python3\n" +
		"import sys\n" +
		"from http.server import HTTPServer, BaseHTTPRequestHandler\n" +
		"class H(BaseHTTPRequestHandler):\n" +
		"    def do_GET(self):\n" +
		"        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')\n" +
		"    def log_message(self, *a): pass\n" +
		"srv = HTTPServer(('127.0.0.1', 0), H)\n" +
		"print('listen=127.0.0.1:%d' % srv.server_address[1], file=sys.stderr, flush=True)\n" +
		"srv.serve_forever()\n")
	art := artifacts.New(dir)
	digest, _, err := art.Put(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p1x", store.FactBuildSucceeded, "i3-pipeline", map[string]any{
		"commit": "1.0.1", "digest": digest, "binary": true,
	}); err != nil {
		t.Fatal(err)
	}

	// 服务跑在这本前缀账本上：单元账本随之隔离。
	b := NewServer(iso, dapr.New(h.ds.URL), NewSessions([]byte("test-key")), "dapr")
	pipe, err := pipeline.New(iso, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipe.Store = art
	b.Pipe = pipe
	s := httptest.NewServer(b.Handler())
	t.Cleanup(s.Close)
	t.Cleanup(pipe.StopAll)

	resp, out := h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/release", map[string]any{
		"env": "SIT", "mode": "full", "units": []string{"p1x"},
	}, RoleSA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("隔离账本上发布应 201，实际 %d：%v", resp.StatusCode, out)
	}
	bid, _ := out["id"].(string)
	units, _ := out["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("批次里应有 1 个单元：%v", out)
	}
	u, _ := units[0].(map[string]any)
	url, _ := u["url"].(string)
	if url == "" {
		t.Fatalf("PBC 应真部署出地址：%v", u)
	}
	if !pipe.Alive("p1x", "SIT") {
		t.Fatal("部署进程应登记在册")
	}
	// 批次在主账本上（公共资产），不在隔离账本上。
	if rv, err := store.ProjectReleases(loadOf(t, h.st, releasesLog)); err != nil || len(rv.Batches) == 0 {
		t.Fatalf("批次应落在主账本：err=%v n=%d", err, len(rv.Batches))
	}
	if rv, err := store.ProjectReleases(loadOf(t, iso, releasesLog)); err != nil || len(rv.Batches) != 0 {
		t.Fatalf("隔离账本不应出现批次：err=%v n=%d", err, len(rv.Batches))
	}

	resp, out = h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/rollback", map[string]any{
		"id": bid, "note": "回滚验收",
	}, RoleSA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回滚应 200（批次在主账本上找得到），实际 %d：%v", resp.StatusCode, out)
	}
	if pipe.Alive("p1x", "SIT") {
		t.Fatal("回滚后部署进程还在登记表里")
	}
	// 单元账本（隔离账本）里那条部署事实已被撤销。
	facts := loadOf(t, iso, "p1x")
	n := 0
	for _, f := range facts {
		if f.Type == store.FactDeploySucceeded {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("单元账本应有 1 条部署事实，实际 %d：%v", n, facts)
	}
	v, err := store.Project("p1x", facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Deploys) != 1 || v.Deploys[0].Status != "rolledback" {
		t.Fatalf("回滚后部署事实应为 rolledback：%+v", v.Deploys)
	}
}

func loadOf(t *testing.T, st *store.Store, pbc string) []store.Fact {
	t.Helper()
	facts, err := st.Load(t.Context(), pbc)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func getJSON(t *testing.T, h *harness, s *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, out := h.doOn(t, s, http.MethodGet, path, nil, RolePA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s 应 200，实际 %d：%v", path, resp.StatusCode, out)
	}
	return out
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		found := false
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
