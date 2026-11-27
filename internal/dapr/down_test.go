package dapr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sidecar 挂了又起来（#55）：读一次 state 时前两次连接被拒、第三次成功，
// 读路径要自己吃下这次抖动，返回值与健康 sidecar 一样。
func TestGetStateRetriesTransientRefusal(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			// 模拟 sidecar 正在重启：直接把连接掐掉。
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Header().Set("ETag", "e1")
		_, _ = w.Write([]byte(`{"v":1}`))
	}))
	defer s.Close()

	raw, etag, err := New(s.URL).GetState(context.Background(), "fake", "k")
	if err != nil {
		t.Fatalf("两次抖动后应该读成功，却报错：%v", err)
	}
	if string(raw) != `{"v":1}` || etag != "e1" {
		t.Fatalf("读到的内容不对：raw=%s etag=%s", raw, etag)
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("应恰好试 3 次，实际 %d 次", n)
	}
}

// 一直连不上：重试 3 次后返回包着 ErrDown 的错误，调用方据此回 503（而不是 500）。
func TestGetStateMarksDownAfterRetries(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := s.URL
	s.Close() // 关掉：连接被拒

	start := time.Now()
	_, _, err := New(url).GetState(context.Background(), "fake", "k")
	if !errors.Is(err, ErrDown) {
		t.Fatalf("应返回 ErrDown，实际：%v", err)
	}
	if el := time.Since(start); el < 180*time.Millisecond {
		t.Fatalf("应在两次重试间等待（约 200ms），实际只用了 %v", el)
	}
	if !strings.Contains(FailHint(err), "与账本失联") {
		t.Fatalf("给用户的提示应说明失联：%q", FailHint(err))
	}
}

// 业务错误（404）不该重试：它不会因为等一会儿就变好，重试只是把错误放大。
func TestGetWorkflowDoesNotRetryBusinessError(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusNotFound)
	}))
	defer s.Close()
	// 404 在 GetWorkflow 里是「实例不存在」→ (nil, nil)，只请求一次。
	st, err := New(s.URL).GetWorkflow(context.Background(), "dapr", "x")
	if err != nil || st != nil {
		t.Fatalf("404 应翻译为实例不存在：st=%v err=%v", st, err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("404 不应重试，实际请求 %d 次", n)
	}
}
