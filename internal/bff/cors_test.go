package bff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #42：跨源带 cookie 读账本必须被浏览器拦下——白名单外的 Origin 一个 CORS 头都不发。
func TestCORSIgnoresUnknownOrigin(t *testing.T) {
	h := NewCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), ParseOrigins("http://127.0.0.1:9999"))

	for _, origin := range []string{"http://evil.example", "", "http://127.0.0.1:9999.attacker"} {
		r := httptest.NewRequest(http.MethodGet, "/api/units", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("origin=%q 不该拿到 Allow-Origin，实际 %q", origin, got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Fatalf("origin=%q 不该拿到 Allow-Credentials，实际 %q", origin, got)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("非跨源请求仍应照常放行，实际 %d", w.Code)
		}
	}
}

// 白名单内的 origin 只回它自己（不反射别的、不回 *），并且必须 Vary: Origin。
func TestCORSAllowsListedOriginOnly(t *testing.T) {
	h := NewCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ParseOrigins(" http://127.0.0.1:9999 , https://proto.example/ "))

	r := httptest.NewRequest(http.MethodOptions, "/api/role", nil)
	r.Header.Set("Origin", "http://127.0.0.1:9999")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("预检应 204，实际 %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:9999" {
		t.Fatalf("Allow-Origin 应只回命中项，实际 %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("命中白名单时带 cookie 才可用，实际 %q", got)
	}
	if !containsVary(w.Header().Values("Vary"), "Origin") {
		t.Fatalf("必须 Vary: Origin，否则中间缓存会串源：%v", w.Header().Values("Vary"))
	}
	// 尾斜杠不该让人以为放行了另一个 origin。
	r.Header.Set("Origin", "https://proto.example")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "https://proto.example" {
		t.Fatalf("配置里的尾斜杠应被收敛掉，实际 %q", got)
	}
}

// 通配与模式一律不接受：写成 "*" 等于又回到对全网开放。
func TestParseOriginsRejectsWildcard(t *testing.T) {
	for _, csv := range []string{"*", "https://*.example.com", " * "} {
		if m := ParseOrigins(csv); len(m) != 0 {
			t.Fatalf("%q 不该被当成白名单，实际 %v", csv, m)
		}
	}
}

func containsVary(vs []string, want string) bool {
	for _, v := range vs {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == want {
				return true
			}
		}
	}
	return false
}
