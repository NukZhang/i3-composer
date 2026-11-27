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

// 同一单元依次发到两个环境（#19）：两个进程必须同时活着，账本两条都是 success、各带自己的环境。
//
// 以前所有环境共用一个 app.bin，第二次部署把第一个环境正在执行的文件覆盖掉，
// 第一个进程当场退出，账本却还记着 deployed。
func TestSameUnitTwoEnvsBothLive(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	iso := store.New(dapr.New(h.ds.URL), "fake", "iso19")
	if err := iso.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, projectsLog, store.FactProjectCreated, "PA", map[string]any{
		"id": "p1", "name": "项目一",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "p19", "name": "p19", "kind": "pbc", "proj": "p1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p19", store.FactCommitCreated, "i3-pipeline", domain.Commit{
		No: "1.0.1", By: "fast", At: "2026-11-27 05:00:00",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p19", store.FactTagCreated, "SA", map[string]any{
		"tag": "v1.0.0", "commit": "1.0.1",
	}); err != nil {
		t.Fatal(err)
	}
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
	art := artifacts.New(t.TempDir())
	digest, _, err := art.Put(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iso.Append(ctx, "p19", store.FactBuildSucceeded, "i3-pipeline", map[string]any{
		"commit": "1.0.1", "digest": digest, "binary": true,
	}); err != nil {
		t.Fatal(err)
	}

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

	urls := map[string]string{}
	for _, env := range []string{"SIT", "UAT"} {
		resp, out := h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/release", map[string]any{
			"env": env, "mode": "full", "units": []string{"p19"},
		}, RoleSA)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("发布到 %s 应 201，实际 %d：%v", env, resp.StatusCode, out)
		}
		units, _ := out["units"].([]any)
		if len(units) != 1 {
			t.Fatalf("%s 批次里应有 1 个单元：%v", env, out)
		}
		u, _ := units[0].(map[string]any)
		urls[env], _ = u["url"].(string)
		if urls[env] == "" {
			t.Fatalf("%s 应真部署出地址：%v", env, u)
		}
	}
	if urls["SIT"] == urls["UAT"] {
		t.Fatalf("两个环境应是两个进程：%s", urls["SIT"])
	}
	// 发布 SIT 之后又发了 UAT：SIT 那个进程必须还活着，而且真的在应答。
	for env, u := range urls {
		if !pipe.Alive("p19", env) {
			t.Fatalf("%s 的进程在另一个环境发布之后没了", env)
		}
		r, err := http.Get(u + "/healthz")
		if err != nil {
			t.Fatalf("%s 的地址应可访问：%v", env, err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("%s 健康检查应 200，实际 %d", env, r.StatusCode)
		}
	}

	// 单元账本：两条部署事实，各带自己的环境，都是 success。
	v, err := store.Project("p19", loadOf(t, iso, "p19"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Deploys) != 2 {
		t.Fatalf("应有两环境各一条部署事实：%+v", v.Deploys)
	}
	seen := map[string]bool{}
	for _, d := range v.Deploys {
		if d.Status != "success" {
			t.Fatalf("两个环境的部署事实都该是 success：%+v", d)
		}
		if d.Env == "" {
			t.Fatalf("部署事实应带环境（#28）：%+v", d)
		}
		seen[d.Env] = true
	}
	if !seen["SIT"] || !seen["UAT"] {
		t.Fatalf("部署事实应分别记在 SIT / UAT 下：%+v", v.Deploys)
	}

	// 编排投影：两个批次都算在线（一个不活就不算整批在线）。
	orch := getJSON(t, h, s, "/api/projects/p1/orch")
	lives, _ := orch["live"].([]any)
	batches, _ := orch["batches"].([]any)
	if len(lives) != len(batches) || len(lives) != 2 {
		t.Fatalf("应有 2 个批次各带一条 live：%v / %v", lives, batches)
	}
	for i, l := range lives {
		if l != true {
			t.Fatalf("第 %d 个批次的进程应在线：%v", i, batches[i])
		}
	}

	// 回滚只收回自己那一批：另一个环境的进程不受影响。
	rv, err := b.loadReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sitBatch string
	for _, bb := range rv.Batches {
		if bb.Env == "SIT" {
			sitBatch = bb.ID
		}
	}
	resp, out := h.doOn(t, s, http.MethodPost, "/api/projects/p1/orch/rollback", map[string]any{
		"id": sitBatch, "note": "只回滚 SIT",
	}, RoleSA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回滚 SIT 应 200，实际 %d：%v", resp.StatusCode, out)
	}
	if pipe.Alive("p19", "SIT") {
		t.Fatal("回滚后 SIT 的进程还在")
	}
	if !pipe.Alive("p19", "UAT") {
		t.Fatal("回滚 SIT 把 UAT 的进程一起收掉了")
	}
}
