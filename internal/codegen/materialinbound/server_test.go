package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func exercise(t *testing.T, h http.Handler, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s 返回 %d，预期 %d：%s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func decodeTest[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func testApp(t *testing.T) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipts.json")
	h, err := newHandler(path, "test", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return h, path
}

func confirmedRequest() inboundInput {
	return inboundInput{RequestID: "inbound-case-01", Type: "RKT-01", SourceNo: "CG-TEST-001", MaterialCode: "MAT-STEEL", WarehouseID: "WH-01", Quantity: "80.400", Unit: "吨"}
}

func TestAT01DefaultsFollowReceiptType(t *testing.T) {
	h, _ := testApp(t)
	out := decodeTest[optionsView](t, exercise(t, h, "GET", "/api/options", nil, 200))
	if len(out.Types) != 2 || out.Types[0].ID != "RKT-01" || out.Types[0].AssetOwner != "公司资产" || out.Types[0].SourceLabel != "采购合同" ||
		out.Types[1].ID != "RKT-02" || out.Types[1].AssetOwner != "甲方资产" || out.Types[1].SourceLabel != "到货点验" ||
		out.Types[0].FinanceTag != "一次性进成本" || out.Types[1].FinanceTag != "一次性进成本" {
		t.Fatalf("两种入库类型及默认值不匹配：%+v", out.Types)
	}
}

func TestAT02ConfirmDeductsAndReplays(t *testing.T) {
	h, path := testApp(t)
	in := confirmedRequest()
	created := decodeTest[receipt](t, exercise(t, h, "POST", "/api/inbounds", in, 201))
	if created.ReceiptNo == "" || created.Quantity != "80.400" || created.RemainingQty != "39.600" {
		t.Fatalf("实收入库和计划余量不一致：%+v", created)
	}
	reopened, err := newHandler(path, "test", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	retry := decodeTest[receipt](t, exercise(t, reopened, "POST", "/api/inbounds", in, 200))
	state := decodeTest[optionsView](t, exercise(t, reopened, "GET", "/api/options", nil, 200))
	if retry != created || state.ReceiptCount != 1 || state.Materials[0].RemainingQty != "39.600" {
		t.Fatalf("重试后多入账或重启丢账：%+v %+v", retry, state)
	}
}

func TestAT03OverPlanAndInvalidInputLeaveLedgerUnchanged(t *testing.T) {
	h, _ := testApp(t)
	in := confirmedRequest()
	in.Quantity = "120.001"
	w := exercise(t, h, "POST", "/api/inbounds", in, 409)
	if !bytes.Contains(w.Body.Bytes(), []byte("超计划")) {
		t.Fatalf("缺少超计划提示：%s", w.Body.String())
	}
	in.Quantity, in.SourceNo = "10", "DY-TEST-001"
	exercise(t, h, "POST", "/api/inbounds", in, 400)
	in.SourceNo, in.Unit = "CG-TEST-001", "千克"
	exercise(t, h, "POST", "/api/inbounds", in, 400)
	state := decodeTest[optionsView](t, exercise(t, h, "GET", "/api/options", nil, 200))
	if state.ReceiptCount != 0 || state.Materials[0].RemainingQty != "120.000" {
		t.Fatalf("失败的入库修改了库存或流水：%+v", state)
	}
}

func TestAT04LedgerTracesSourceAndClassification(t *testing.T) {
	h, _ := testApp(t)
	in := confirmedRequest()
	in.Type, in.SourceNo = "RKT-02", "DY-TEST-001"
	created := decodeTest[receipt](t, exercise(t, h, "POST", "/api/inbounds", in, 201))
	got := decodeTest[receipt](t, exercise(t, h, "GET", "/api/inbounds/"+created.ReceiptNo, nil, 200))
	if got != created || got.SourceNo != in.SourceNo || got.MaterialCode != in.MaterialCode || got.Quantity != in.Quantity ||
		got.Unit != in.Unit || got.AssetOwner != "甲方资产" || got.FinanceTag != "一次性进成本" {
		t.Fatalf("台账缺少来源与业务快照：%+v", got)
	}
	exercise(t, h, "GET", "/api/inbounds/RK-missing", nil, 404)
}

func TestAT05HealthIdentifiesCurrentProcess(t *testing.T) {
	h, _ := testApp(t)
	got := decodeTest[map[string]any](t, exercise(t, h, "GET", "/healthz", nil, 200))
	if got["ok"] != true || got["unit"] != unitID || got["commit"] != "1.0.1" {
		t.Fatalf("健康检查缺少制品身份：%+v", got)
	}
}
