package bff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/store"
)

// fakeDapr 记录 BFF 发往 sidecar 的调用，用于断言「权限拒绝时不发 start/raiseEvent」。
type fakeDapr struct {
	mu       sync.Mutex
	calls    []string
	data     map[string][]byte
	etags    map[string]int
	conflict bool
}

func newFake() *fakeDapr {
	return &fakeDapr{data: map[string][]byte{}, etags: map[string]int{}}
}

func (f *fakeDapr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/v1.0/state/fake/"), "%3A", ":")

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.0/state/"):
		b, ok := f.data[key]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("ETag", "e"+string(rune('0'+f.etags[key]%10)))
		_, _ = w.Write(b)

	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1.0/state/"):
		var items []struct {
			Key     string          `json:"key"`
			Value   json.RawMessage `json:"value"`
			Etag    string          `json:"etag"`
			Options struct {
				Concurrency string `json:"concurrency"`
			} `json:"options"`
		}
		_ = json.NewDecoder(r.Body).Decode(&items)
		for _, it := range items {
			if it.Options.Concurrency == dapr.ConcurrencyFirstWrite && it.Etag != "" &&
				it.Etag != "e"+string(rune('0'+f.etags[it.Key]%10)) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.data[it.Key] = it.Value
			f.etags[it.Key]++
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeDapr) tookCall(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

type harness struct {
	srv  *httptest.Server
	fake *fakeDapr
	st   *store.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFake()
	ds := httptest.NewServer(f)
	t.Cleanup(ds.Close)
	st := store.New(dapr.New(ds.URL), "fake")
	b := NewServer(st, dapr.New(ds.URL), NewSessions([]byte("test-key")), "dapr")
	s := httptest.NewServer(b.Handler())
	t.Cleanup(s.Close)
	return &harness{srv: s, fake: f, st: st}
}

func (h *harness) do(t *testing.T, method, path string, body any, role Role) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		// 用真实的签名 cookie，确保测试走的是生产同一条权限路径。
		rec := httptest.NewRecorder()
		NewSessions([]byte("test-key")).Issue(rec, role)
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func seedItem(t *testing.T, st *store.Store, it store.Item) {
	t.Helper()
	ctx := t.Context()
	if err := st.Ensure(ctx, "fee"); err != nil {
		t.Fatal(err)
	}
	if it.Steps == nil {
		it.Steps = store.StepPlan(it.Env)
	}
	if _, err := st.Append(ctx, "fee", store.FactItemCreated, "BA", it); err != nil {
		t.Fatal(err)
	}
}

func TestRoleCannotCreateTaskWithoutBacklogAct(t *testing.T) {
	h := newHarness(t)
	// DA 没有 backlog 权限。
	resp, out := h.do(t, http.MethodPost, "/api/tasks?pbc=fee", map[string]string{"title": "x"}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 建任务应 403，实际 %d %v", resp.StatusCode, out)
	}
	// SA 也没有。
	resp, _ = h.do(t, http.MethodPost, "/api/tasks?pbc=fee", map[string]string{"title": "x"}, RoleSA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("SA 建任务应 403，实际 %d", resp.StatusCode)
	}
	// BA 有。
	resp, out = h.do(t, http.MethodPost, "/api/tasks?pbc=fee", map[string]string{"title": "业主账单自助查询"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("BA 建任务应 201，实际 %d %v", resp.StatusCode, out)
	}
	if out["id"] != "US-01" {
		t.Fatalf("首个工作项应是 US-01，实际 %v", out["id"])
	}
}

func TestAnswerRequiresTagActAndCorrectGate(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "waiting", Step: 6, By: "PA"})

	// DA 无 tag 权限 → 403，且绝不能把事件发给 sidecar。
	resp, _ := h.do(t, http.MethodPost, "/api/items/US-01/answer?pbc=fee",
		map[string]any{"approve": true}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DA 拍板应 403，实际 %d", resp.StatusCode)
	}
	if h.fake.tookCall("raiseEvent") {
		t.Fatal("权限拒绝时不应向 sidecar 发 raiseEvent")
	}
	if h.fake.tookCall("/start") {
		t.Fatal("权限拒绝时不应向 sidecar 发 start")
	}

	// 请求体里伪造 role/canApprove 不得生效。
	resp, _ = h.do(t, http.MethodPost, "/api/items/US-01/answer?pbc=fee",
		map[string]any{"approve": true, "role": "SA", "canApprove": true}, RoleDA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("伪造角色也不该放行，实际 %d", resp.StatusCode)
	}

	// SA 有 tag 权限 → 接受。
	resp, out := h.do(t, http.MethodPost, "/api/items/US-01/answer?pbc=fee",
		map[string]any{"approve": true}, RoleSA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("SA 拍板应 202，实际 %d %v", resp.StatusCode, out)
	}
	if !h.fake.tookCall("raiseEvent") {
		t.Fatal("应当向 sidecar 发 raiseEvent")
	}

	// 不在发布闸的项不能拍板。
	seedItem(t, h.st, store.Item{ID: "US-02", Title: "t2", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})
	resp, _ = h.do(t, http.MethodPost, "/api/items/US-02/answer?pbc=fee",
		map[string]any{"approve": true}, RoleSA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未到发布闸时拍板应 400，实际 %d", resp.StatusCode)
	}
}

func TestRunItemUsesItemIDAsInstanceLock(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	// BA 无 build 权限。
	resp, _ := h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleBA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("BA 发起构建应 403，实际 %d", resp.StatusCode)
	}

	resp, out := h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("DA 发起构建应 202，实际 %d %v", resp.StatusCode, out)
	}
	if !strings.Contains(out["instance"].(string), "US-01") {
		t.Fatalf("实例 ID 必须等于工作项 ID（这是并发锁），实际 %v", out["instance"])
	}

	// 同一个 item 再发起：已在执行中 → 400，且不应再打 start。
	before := len(h.fake.calls)
	resp, _ = h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("重复发起应 400，实际 %d", resp.StatusCode)
	}
	if len(h.fake.calls) != before {
		t.Fatal("被拒绝的重复发起不应再调 sidecar")
	}
}

func TestOverviewAndTaskFilters(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "待我确认", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "waiting", Step: 6, By: "PA"})
	seedItem(t, h.st, store.Item{ID: "US-02", Title: "DA 的活", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "running", Step: 2, Owner: "DA", By: "BA"})

	_, out := h.do(t, http.MethodGet, "/api/overview?pbc=fee", nil, RoleDA)
	if out["role"] != "DA" {
		t.Fatalf("会话角色应来自 cookie，实际 %v", out["role"])
	}

	_, out = h.do(t, http.MethodGet, "/api/tasks?pbc=fee&tab=review", nil, RoleDA)
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "US-01" {
		t.Fatalf("待我确认应只有 US-01，实际 %v", items)
	}

	_, out = h.do(t, http.MethodGet, "/api/tasks?pbc=fee&tab=todo", nil, RoleDA)
	items = out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "US-02" {
		t.Fatalf("DA 的待办应只有 US-02，实际 %v", items)
	}

	// 无 cookie 默认 BA（只读起点），不得凭空获得高权限。
	_, out = h.do(t, http.MethodGet, "/api/overview?pbc=fee", nil, "")
	if out["role"] != "BA" {
		t.Fatalf("无会话时应默认 BA，实际 %v", out["role"])
	}
}

func TestSetRoleRejectsUnknown(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodPost, "/api/role", map[string]string{"role": "ADMIN"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知角色应 400，实际 %d", resp.StatusCode)
	}
	resp, out := h.do(t, http.MethodPost, "/api/role", map[string]string{"role": "SA"}, "")
	if resp.StatusCode != http.StatusOK || out["role"] != "SA" {
		t.Fatalf("切换 SA 应成功，实际 %d %v", resp.StatusCode, out)
	}
}

func TestCreateTaskValidatesTitle(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodPost, "/api/tasks?pbc=fee", map[string]string{"title": "   "}, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空标题应 400，实际 %d", resp.StatusCode)
	}
	long := strings.Repeat("长", 81)
	resp, _ = h.do(t, http.MethodPost, "/api/tasks?pbc=fee", map[string]string{"title": long}, RoleBA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超长标题应 400，实际 %d", resp.StatusCode)
	}
}
func TestRunRejectsPublishedItem(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-09", Title: "已上线", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "published",
		Tag: "v1.0.0", Version: "1.0.2", URL: "https://x", By: "SA"})

	before := len(h.fake.calls)
	resp, out := h.do(t, http.MethodPost, "/api/items/US-09/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("已发布项再发起应 400，实际 %d %v", resp.StatusCode, out)
	}
	if len(h.fake.calls) != before {
		t.Fatal("被拒绝的发起绝不能调 sidecar 起实例")
	}
}
