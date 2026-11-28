package bff

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrototypeSnapshotReportsUnavailableRenderer(t *testing.T) {
	_, _, handler, _ := prototypeReviewFixture(t)
	data := []byte(`<!doctype html><div id="app"></div><script>document.querySelector('#app').textContent = '业务画面'</script>`)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "dynamic.html", data, RolePA), http.StatusCreated)
	response := prototypeReviewRequest(handler, "GET", "/api/prototype-imports/1/snapshot?pbc=fee", nil, "", RoleBA, nil)
	reviewStatus(t, response, http.StatusServiceUnavailable)
}

func TestPrototypeSnapshotScopesAndPinsRenderedImage(t *testing.T) {
	s, _, handler, _ := prototypeReviewFixture(t)
	data := []byte(`<!doctype html><div id="app"></div><script>document.querySelector('#app').textContent = '业务画面'</script>`)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "dynamic.html", data, RolePA), http.StatusCreated)
	imageData := prototypeReviewImage(t, func(w *bytes.Buffer, img image.Image) error { return png.Encode(w, img) })
	var calls atomic.Int32
	s.PrototypeRenderSocket = previewTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, data) || r.Method != http.MethodPost || r.URL.Path != "/render" || r.URL.RawQuery != "" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Error("渲染只应接收原件及固定的 POST /render 请求")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	const endpoint = "/api/prototype-imports/1/snapshot"
	get := func(query string) *httptest.ResponseRecorder {
		return prototypeReviewRequest(handler, "GET", endpoint+query, nil, "", RoleBA, map[string]string{"Authorization": "test-session"})
	}
	reviewStatus(t, get(""), http.StatusBadRequest)
	reviewStatus(t, get("?pbc=other"), http.StatusNotFound)
	if calls.Load() != 0 {
		t.Fatal("越界请求不能送入渲染器")
	}
	for i := 0; i < 2; i++ {
		response := get("?pbc=fee")
		reviewStatus(t, response, http.StatusOK)
		if !bytes.Equal(response.Body.Bytes(), imageData) || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("工作台必须拿到固定的只读 PNG")
		}
		// 渲染器退出后缓存仍可读，线条可以重复落在同一图片上。
		s.PrototypeRenderSocket = ""
	}
	if calls.Load() != 1 {
		t.Fatalf("预览刷新不应重跑脚本，实际 %d 次", calls.Load())
	}
	response := prototypeReviewRequest(handler, "GET", "/api/prototype-imports/1?pbc=fee", nil, "", RoleBA, nil)
	if !bytes.Equal(response.Body.Bytes(), data) || !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'none'") {
		t.Fatal("原件仍应按原字节和禁用脚本的沙箱返回")
	}
}

func TestPrototypeSnapshotRejectsNonImageResponse(t *testing.T) {
	s, _, handler, _ := prototypeReviewFixture(t)
	reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "dynamic.html", []byte("<!doctype html><script>0</script>"), RolePA), http.StatusCreated)
	s.PrototypeRenderSocket = previewTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "<script>必须拒绝这种返回</script>")
	}))
	response := prototypeReviewRequest(handler, "GET", "/api/prototype-imports/1/snapshot?pbc=fee", nil, "", RoleBA, nil)
	reviewStatus(t, response, http.StatusUnprocessableEntity)
}

func previewTestSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	// macOS 的 Unix socket 路径上限短于 Go 默认的测试临时目录。
	dir, err := os.MkdirTemp("/tmp", "i3-snapshot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "r.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(l) }()
	return path
}
