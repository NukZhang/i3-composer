package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/store"
)

// sidecarWithWorkflow 是一个只回答 workflow 查询的 sidecar 替身（#53 对账用）。
// state 返回当前的实例终态；nil 表示「查不到这个实例」。
func sidecarWithWorkflow(t *testing.T, status map[string]string) *httptest.Server {
	t.Helper()
	// 状态存储交给测试里已有的 fake（etag / 键编码都与生产同构），只有 workflow 查询走本替身。
	state := newFake()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1.0/workflows/") {
			state.ServeHTTP(w, r)
			return
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := status[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

// 发起过（item.run.started）但 workflow 已经 FAILED 的工作项：读一次 /api/state
// 必须把它对账成 failed 并落 item.run.failed；再读一次不能重复落（#53）。
func TestStateReconcilesFailedWorkflow(t *testing.T) {
	failed := `{"instanceID":"fee_US-01","runtimeStatus":"FAILED","properties":{"dapr.workflow.failure.error_message":"workflow named 'x' is not registered"}}`
	running := `{"instanceID":"fee_US-02","runtimeStatus":"RUNNING"}`
	ds := sidecarWithWorkflow(t, map[string]string{"fee_US-01": failed, "fee_US-02": running})

	st := store.New(dapr.New(ds.URL), "fake")
	b := NewServer(st, dapr.New(ds.URL), NewSessions([]byte("test-key")), "dapr")
	srv := httptest.NewServer(b.Handler())
	t.Cleanup(srv.Close)

	registerUnit(t, st, "fee")
	seedItem(t, st, admittedItem())
	it2 := admittedItem()
	it2.ID = "US-02"
	seedItem(t, st, it2)
	for _, id := range []string{"US-01", "US-02"} {
		if _, err := st.Append(t.Context(), "fee", store.FactItemRunStarted, "DA", map[string]any{"id": id}); err != nil {
			t.Fatal(err)
		}
	}

	get := func() store.Item {
		resp, err := http.Get(srv.URL + "/api/state?pbc=fee")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Items map[string]store.Item `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Items["US-01"]
	}

	it := get()
	if it.Run != "failed" || !strings.Contains(it.Failed, "is not registered") || it.FailedStatus != "FAILED" {
		t.Fatalf("FAILED 的实例应被对账成 failed 且带原因，实际 run=%q failed=%q status=%q", it.Run, it.Failed, it.FailedStatus)
	}
	// 还在跑的不能被写成失败：查得到 RUNNING 就照旧 running。
	if v, _ := store.Project("fee", mustLoad(t, st)); v.Items["US-02"].Run != "running" {
		t.Fatalf("RUNNING 的实例不该被改状态，实际 %q", v.Items["US-02"].Run)
	}

	get() // 第二次读：不该再落一条
	n := 0
	for _, f := range mustLoad(t, st) {
		if f.Type == store.FactItemRunFailed {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("item.run.failed 应只落一条，实际 %d 条", n)
	}
}

func mustLoad(t *testing.T, st *store.Store) []store.Fact {
	t.Helper()
	facts, err := st.Load(t.Context(), "fee")
	if err != nil {
		t.Fatal(err)
	}
	return facts
}
