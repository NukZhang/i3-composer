package bff

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/dapr"
)

// 这一组测的是验收里最容易看走眼的两处：
//   - sidecar 的 app-port 与本进程端口不一致时，pubsub 事件投不到本进程；
//   - 随机生成的 id 撞号后，第二条记录会静默覆盖第一条。

func TestPortOfAddr(t *testing.T) {
	cases := map[string]int{
		":8080":          8080,
		"127.0.0.1:8090": 8090,
		"localhost:3500": 3500,
		":":              0,
		"":               0,
		":abc":           0,
		":8080junk":      0,
		":65536":         0,
		"[::1]:8080":     8080,
	}
	for addr, want := range cases {
		if got := PortOfAddr(addr); got != want {
			t.Errorf("PortOfAddr(%q) = %d, want %d", addr, got, want)
		}
	}
}

func TestUniqueID(t *testing.T) {
	none := map[string]bool{}
	if got := uniqueID("p1a2", none); got != "p1a2" {
		t.Errorf("空闲时不该改名：got %q", got)
	}
	used := map[string]bool{"p1a2": true}
	if got := uniqueID("p1a2", used); got != "p1a20" {
		t.Errorf("撞号时该退到 p1a20：got %q", got)
	}
	used2 := map[string]bool{"p1a2": true, "p1a20": true}
	if got := uniqueID("p1a2", used2); got != "p1a21" {
		t.Errorf("连续撞号要一路退到 p1a21：got %q", got)
	}
	if uniqueID("p1a2", used2) == "p1a2" || uniqueID("p1a2", used2) == "p1a20" {
		t.Errorf("返回了已占用的 id")
	}
}

// fakeSidecar 起一个只回 /v1.0/metadata 的假 sidecar。
func fakeSidecar(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1.0/metadata") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

const metadata8080 = `{"appConnectionProperties":{"port":8080,"protocol":"http","channelAddress":"host.docker.internal"}}`

func TestCheckPubsubWiring(t *testing.T) {
	ts := fakeSidecar(t, metadata8080, http.StatusOK)
	defer ts.Close()

	same := &Server{Dapr: dapr.New(ts.URL), LocalPort: 8080}
	got := same.CheckPubsubWiring(context.Background())
	if got["checked"] != true || got["ok"] != true {
		t.Fatalf("端口一致时应 ok：%v", got)
	}
	if got["sidecarAppPort"] != 8080 {
		t.Errorf("应读出 sidecar 的 app-port：%v", got)
	}

	diff := &Server{Dapr: dapr.New(ts.URL), LocalPort: 8090}
	got = diff.CheckPubsubWiring(context.Background())
	if got["checked"] != true || got["ok"] != false {
		t.Fatalf("端口不一致时应判红：%v", got)
	}
	detail, _ := got["detail"].(string)
	if !strings.Contains(detail, "8090") || !strings.Contains(detail, "8080") {
		t.Errorf("detail 应把两个端口都写出来，方便一眼定位：%q", detail)
	}

	// sidecar 没起 / 元数据读不到：不判红，但明确标出「没校验」。
	broken := &Server{Dapr: dapr.New("http://127.0.0.1:1"), LocalPort: 8080}
	got = broken.CheckPubsubWiring(context.Background())
	if got["checked"] != false || got["ok"] != true {
		t.Fatalf("读不到元数据时不该判红：%v", got)
	}

	// 监听地址里没有端口：跳过校验，不能瞎判。
	unknown := &Server{Dapr: dapr.New(ts.URL), LocalPort: 0}
	got = unknown.CheckPubsubWiring(context.Background())
	if got["checked"] != false || got["ok"] != true {
		t.Fatalf("端口未知时不该判红：%v", got)
	}
}
