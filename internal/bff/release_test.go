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
