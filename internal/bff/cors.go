package bff

import (
	"net/http"
	"strings"
)

// CORS 只对显式配置的来源放行。
//
// 以前这里反射请求里的 Origin，还无条件带 Allow-Credentials: true——那一对组合
// 等于对任何网站开放「用受害者的会话 cookie 跨源读全部账本」（#42）。注释写的是
// 「不带凭据通配」，代码做的正好相反。会话是唯一凭据（宪法 V），那就不能让
// 任何页面拿着浏览器里现成的会话去读。
//
// 默认 origins 为空 = 不发任何 Access-Control-* 头：同源使用（页面由本服务托管）
// 本来就不需要 CORS。原型页要跨源调试时，用 CORS_ORIGINS 显式列出精确 origin。
type CORS struct {
	origins map[string]bool
	next    http.Handler
}

// ParseOrigins 把逗号分隔的配置收敛成集合。只认精确 origin（scheme://host[:port]），
// 不接受 "*" 或任何模式：配置错了宁可不放行，也不要一次想当然的通配。
func ParseOrigins(csv string) map[string]bool {
	m := map[string]bool{}
	for _, o := range strings.Split(csv, ",") {
		o = strings.TrimSpace(o)
		if o == "" || o == "*" || strings.ContainsAny(o, "* ") {
			continue
		}
		m[strings.TrimRight(o, "/")] = true
	}
	return m
}

// NewCORS 包一层：白名单外照样放行请求（同源、curl、服务间调用不受影响），
// 只是不回 CORS 头——浏览器的跨源脚本因此读不到响应。
func NewCORS(h http.Handler, origins map[string]bool) *CORS {
	return &CORS{origins: origins, next: h}
}

func (c *CORS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if origin := strings.TrimRight(r.Header.Get("Origin"), "/"); origin != "" && c.origins[origin] {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		// 必须带 Vary：同一个 URL 对不同 Origin 的头不同，不让中间缓存把 A 的
		// 响应喂给 B。
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c.next.ServeHTTP(w, r)
}
