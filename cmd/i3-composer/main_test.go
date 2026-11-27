package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhangkun/i3-composer/internal/bff"
	"github.com/zhangkun/i3-composer/internal/dapr"
)

func writeWebIndex(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWebDir(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeWebIndex(t, "web/dist")
	t.Setenv("WEB_DIR", "")
	if dir, err := resolveWebDir(); err != nil || dir != filepath.Join(cwd, "web", "dist") {
		t.Fatalf("默认目录 = %q, err = %v", dir, err)
	}
	override := t.TempDir()
	writeWebIndex(t, override)
	t.Setenv("WEB_DIR", override)
	if dir, err := resolveWebDir(); err != nil || dir != override {
		t.Fatalf("配置目录 = %q, err = %v", dir, err)
	}
	t.Setenv("WEB_DIR", "missing")
	if _, err := resolveWebDir(); err == nil || !strings.Contains(err.Error(), filepath.Join(cwd, "missing", "index.html")) {
		t.Fatalf("缺少入口时应报告绝对路径，实际 %v", err)
	}
	if err := os.MkdirAll("not-file/index.html", 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEB_DIR", "not-file")
	if _, err := resolveWebDir(); err == nil || !strings.Contains(err.Error(), "不是普通文件") {
		t.Fatalf("前端入口是目录时应立即拒绝，实际 %v", err)
	}
}

func TestWaitForPubsubWiringRetriesUntilSidecarReady(t *testing.T) {
	calls := make(chan struct{}, 1)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case calls <- struct{}{}:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			fmt.Fprint(w, `{"appConnectionProperties":{"port":8080}}`)
		}
	}))
	defer sidecar.Close()
	server := &bff.Server{LocalPort: 8080, Dapr: dapr.New(sidecar.URL)}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := waitForPubsubWiring(ctx, server); err != nil {
		t.Fatalf("sidecar 稍晚就绪后应通过严格校验：%v", err)
	}
}

func TestRunRejectsMissingWebIndex(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WEB_DIR", dir)
	t.Setenv("DAPR_HTTP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("DAPR_GRPC_ENDPOINT", "127.0.0.1:1")
	err := run()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "index.html")) {
		t.Fatalf("应在连接 sidecar 前报告缺少前端入口，实际 %v", err)
	}
}

func TestRunRequiresPubsubAtStartup(t *testing.T) {
	dir := t.TempDir()
	writeWebIndex(t, dir)
	t.Setenv("WEB_DIR", dir)
	t.Setenv("ADDR", "127.0.0.1:8181")
	t.Setenv("DAPR_GRPC_ENDPOINT", "127.0.0.1:1")
	t.Setenv("REQUIRE_PUBSUB", "1")
	cases := []struct {
		name   string
		status int
		detail string
	}{
		{name: "端口不匹配", status: http.StatusOK, detail: "app-port=8080，本实例监听 8181"},
		{name: "无法核对", status: http.StatusServiceUnavailable, detail: "读不到 sidecar 元数据"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1.0/metadata" {
					t.Errorf("校验失败前不应触发其他 sidecar 请求：%s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"appConnectionProperties":{"port":8080}}`)
			}))
			defer sidecar.Close()
			t.Setenv("DAPR_HTTP_ENDPOINT", sidecar.URL)
			err := run()
			if err == nil || !strings.Contains(err.Error(), "REQUIRE_PUBSUB=1") || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("应在 workflow 和种子前退出并说明原因，实际 %v", err)
			}
		})
	}
}
