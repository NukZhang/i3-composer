package bff

import (
	"net/http"
	"testing"
)

// sidecar 失联（#55）：读接口回 503 + retryable + degraded=dapr，
// /api/health 立刻说 ok=false 并点名 dapr；不是 500，也不是假装健康。
func TestReadsDegradeWhenSidecarDown(t *testing.T) {
	h := newHarness(t)
	registerUnit(t, h.st, "fee")
	h.ds.Close() // sidecar 没了

	resp, out := h.do(t, http.MethodGet, "/api/units", nil, RolePA)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("sidecar 失联时应回 503，实际 %d：%v", resp.StatusCode, out)
	}
	if out["retryable"] != true || out["degraded"] != "dapr" {
		t.Fatalf("503 应带 retryable=true、degraded=dapr：%v", out)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("503 应带 Retry-After，客户端才知道多久后再试")
	}

	resp, out = h.do(t, http.MethodGet, "/api/health", nil, RolePA)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("失联时 health 应回 503，实际 %d：%v", resp.StatusCode, out)
	}
	if out["ok"] != false {
		t.Fatalf("失联时 health.ok 必须是 false：%v", out)
	}
	d, _ := out["dapr"].(map[string]any)
	if d == nil || d["ok"] != false || d["retryable"] != true {
		t.Fatalf("health.dapr 应点名失联且可重试：%v", out["dapr"])
	}
}

// 正常时 health 读得到账本：ok=true、dapr.ok=true、200。
func TestHealthProbesLedger(t *testing.T) {
	h := newHarness(t)
	resp, out := h.do(t, http.MethodGet, "/api/health", nil, RolePA)
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("sidecar 正常时 health 应 200/ok：%d %v", resp.StatusCode, out)
	}
	d, _ := out["dapr"].(map[string]any)
	if d == nil || d["ok"] != true || d["checked"] != true {
		t.Fatalf("health.dapr 应真的探过一次：%v", out["dapr"])
	}
}
