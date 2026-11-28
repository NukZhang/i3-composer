package bff

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrototypeInteractiveIsBoundToImportAndKeepsLedgerUnchanged(t *testing.T) {
	s, _, handler, _ := prototypeReviewFixture(t)
	source := []byte("<!doctype html><p>原型交互</p>")
	for range 2 {
		reviewStatus(t, uploadPrototypeFixture(t, handler, "?pbc=fee", "interaction.html", source, RolePA), http.StatusCreated)
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1200, 800))); err != nil {
		t.Fatal(err)
	}
	const id = "c1b02b9c120543e793c89f20687f8f72"
	var calls atomic.Int32
	s.PrototypeRenderSocket = previewTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("交互渲染不接收工作台身份或查询参数")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "image/png")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/interactive":
			if !bytes.Equal(raw, source) || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
				t.Error("新会话只发送原件")
			}
			w.Header().Set("X-Prototype-Session", id)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/interactive/"+id:
			if string(raw) != `{"type":"text","text":"试填"}` {
				t.Errorf("预期只发送校验后的表单输入，收到 %q", raw)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/interactive/"+id:
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			t.Errorf("不应访问 %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(picture.Bytes())
	}))

	request := func(method, endpoint, payload string, headers map[string]string) *httptest.ResponseRecorder {
		return prototypeReviewRequest(handler, method, endpoint, strings.NewReader(payload), "application/json", RoleBA, headers)
	}
	state := func() [32]byte {
		res := request(http.MethodGet, "/api/state?pbc=fee", "", nil)
		reviewStatus(t, res, http.StatusOK)
		return sha256.Sum256(res.Body.Bytes())
	}
	before := state()
	const endpoint = "/api/prototype-imports/1/interactive"
	reviewStatus(t, request(http.MethodPost, endpoint, "{}", nil), http.StatusBadRequest)
	reviewStatus(t, request(http.MethodPost, endpoint+"?pbc=fee", "{}", map[string]string{"Sec-Fetch-Site": "cross-site"}), http.StatusForbidden)
	first := request(http.MethodPost, endpoint+"?pbc=fee", "{}", map[string]string{"Authorization": "test"})
	reviewStatus(t, first, http.StatusCreated)
	token := first.Header().Get("X-Prototype-Session")
	if token == "" || token == id || first.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(first.Body.Bytes(), picture.Bytes()) {
		t.Fatal("应返回绑定导入范围的凭证和不可缓存的 PNG")
	}
	actionPath := endpoint + "/" + token
	reviewStatus(t, request(http.MethodPost, "/api/prototype-imports/2/interactive/"+token+"?pbc=fee", `{"type":"frame"}`, nil), http.StatusNotFound)
	reviewStatus(t, request(http.MethodPost, actionPath+"x?pbc=fee", `{"type":"frame"}`, nil), http.StatusNotFound)
	reviewStatus(t, request(http.MethodPost, actionPath+"?pbc=fee", `{"type":"click","x":-0.1,"y":0.5}`, nil), http.StatusBadRequest)
	reviewStatus(t, request(http.MethodPost, actionPath+"?pbc=fee", `{"type":"key","key":"Control+L"}`, nil), http.StatusBadRequest)
	if calls.Load() != 1 {
		t.Fatal("无效请求不应送进沙箱")
	}
	frame := request(http.MethodPost, actionPath+"?pbc=fee", `{"type":"text","text":"试填"}`, nil)
	reviewStatus(t, frame, http.StatusOK)
	if !bytes.Equal(frame.Body.Bytes(), picture.Bytes()) {
		t.Fatal("动作响应必须返回 PNG 画面")
	}
	reviewStatus(t, request(http.MethodDelete, actionPath+"?pbc=fee", "", nil), http.StatusNoContent)
	if calls.Load() != 3 || state() != before {
		t.Fatal("交互只应访问隔离渲染器，工程账本不应改变")
	}
}
