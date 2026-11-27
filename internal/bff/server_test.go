package bff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	ds   *httptest.Server // sidecar 的替身：要给另一份 Server 接线时用
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFake()
	ds := httptest.NewServer(f)
	t.Cleanup(ds.Close)
	st := store.New(dapr.New(ds.URL), "fake")
	b := NewServer(st, dapr.New(ds.URL), NewSessions([]byte("test-key")), "dapr")
	// BFF 与生产同构：事实落链后应推 SSE。测试里把这条回调接上，才能验证通知链。
	st.Notify = b.NotifyChange
	s := httptest.NewServer(b.Handler())
	t.Cleanup(s.Close)
	return &harness{srv: s, fake: f, st: st, ds: ds}
}

// registerUnit 在 units 注册表里登记一个单元：/api/state 只对登记过的 id 出投影（#43）。
func registerUnit(t *testing.T, st *store.Store, id string) {
	t.Helper()
	ctx := t.Context()
	if err := st.Ensure(ctx, "units"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, "units", store.FactUnitCreated, "PA", map[string]any{
		"id": id, "name": id, "kind": "pbc",
	}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) do(t *testing.T, method, path string, body any, role Role) (*http.Response, map[string]any) {
	t.Helper()
	return h.doOn(t, h.srv, method, path, body, role)
}

// doOn 对指定服务打请求：测「另一份装配」时不能都走 harness 自己那台。
func (h *harness) doOn(t *testing.T, srv *httptest.Server, method, path string, body any, role Role) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, srv.URL+path, rd)
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

// admittedItem 是一条「入口准入放得过」的工作项：验收口径可断言、
// 写了数据范围与两个责任人。它只差软项（没拆 task），所以发起会带 waiver。
func admittedItem() store.Item {
	return store.Item{
		ID: "US-01", Title: "业主查看并支付账单", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA",
		Accept:   []string{"当业主进入账单页，则应看到本期应缴金额与到期日"},
		Data:     []string{"fee.bill.amount"},
		AcceptBy: "BA 业务", ComplyBy: "SA 系统",
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

func TestRetiredItemAnswerHasNoSideEffects(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "历史发布工作项", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "waiting", Step: 6, By: "PA"})
	seedItem(t, h.st, store.Item{ID: "US-02", Title: "当前交付工作项", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "verified", Step: 5, By: "BA"})
	before, err := h.st.Load(t.Context(), "fee")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"US-01", "US-02"} {
		resp, out := h.do(t, http.MethodPost, "/api/items/"+id+"/answer?pbc=fee",
			map[string]bool{"approve": true}, RoleSA)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("已停用的路由应返回 404，工作项 %s 实际 %d %v", id, resp.StatusCode, out)
		}
	}
	after, err := h.st.Load(t.Context(), "fee")
	if err != nil || len(after) != len(before) || h.fake.tookCall("raiseEvent") {
		t.Fatalf("旧入口不应写账本或发事件：before=%d after=%d err=%v", len(before), len(after), err)
	}
}

func TestRunItemUsesItemIDAsInstanceLock(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, admittedItem())

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

// 入口准入：硬项不过 → 400，点名「缺什么」，且账本不留 item.run.started、不调 sidecar。
func TestRunItemRejectedByAdmission(t *testing.T) {
	h := newHarness(t)
	// 只有标题、没有任何准入输入：三项硬项全缺。
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "优化一下", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	before := len(h.fake.calls)
	resp, out := h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("准入不过应 400，实际 %d %v", resp.StatusCode, out)
	}
	msg, _ := out["error"].(string)
	for _, want := range []string{"目标可断言", "数据与权限", "责任人", "找 BA 业务", "找 SA 系统"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("400 应点名缺什么、找谁，缺 %q；实际：%s", want, msg)
		}
	}
	if len(h.fake.calls) != before {
		t.Fatal("准入拒绝时绝不能调 sidecar 起实例")
	}

	// 账本里有 rejected 事实，但没有 run.started。
	facts, err := h.st.Load(t.Context(), "fee")
	if err != nil {
		t.Fatal(err)
	}
	var rejected, started bool
	for _, f := range facts {
		switch f.Type {
		case store.FactItemAdmitRejected:
			rejected = true
		case store.FactItemRunStarted:
			started = true
		}
	}
	if !rejected {
		t.Fatal("被拒的发起应落一条 item.admit.rejected")
	}
	if started {
		t.Fatal("被拒的发起绝不能落 item.run.started（不留半条链）")
	}
}

// 硬项补齐后可以再次发起：补输入走 /inputs，发起时重判。
func TestAdmitInputsThenRun(t *testing.T) {
	h := newHarness(t)
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "优化一下", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	if resp, out := h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleDA); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("先应被准入拒绝，实际 %d %v", resp.StatusCode, out)
	}
	// DA 没有 backlog 权限，补输入应 403。
	if resp, _ := h.do(t, http.MethodPost, "/api/items/US-01/inputs?pbc=fee", map[string]any{
		"acceptBy": "BA 业务", "complyBy": "SA 系统",
	}, RoleDA); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("补输入应仅 BA 可做，实际 %d", resp.StatusCode)
	}
	// BA 补三项硬项输入。
	resp, out := h.do(t, http.MethodPost, "/api/items/US-01/inputs?pbc=fee", map[string]any{
		"accept":   []string{"当业主进入账单页，则应看到应缴金额"},
		"data":     []string{"fee.bill.amount"},
		"acceptBy": "BA 业务", "complyBy": "SA 系统",
	}, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("补输入应 200，实际 %d %v", resp.StatusCode, out)
	}
	// 再发起：硬项齐了，放行。
	resp, out = h.do(t, http.MethodPost, "/api/items/US-01/run?pbc=fee", nil, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("补齐后应 202，实际 %d %v", resp.StatusCode, out)
	}
	// 放行事实带 waiver：这是 Story 没拆 task，软项不过但放行。
	facts, err := h.st.Load(t.Context(), "fee")
	if err != nil {
		t.Fatal(err)
	}
	var admitted bool
	for _, f := range facts {
		if f.Type == store.FactItemAdmitted {
			admitted = true
			if !strings.Contains(string(f.Payload), "waivers") {
				t.Fatalf("item.admitted 应带 waivers 字段：%s", string(f.Payload))
			}
		}
	}
	if !admitted {
		t.Fatal("放行应落 item.admitted")
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

func TestStateReturnsFullProjection(t *testing.T) {
	h := newHarness(t)
	// #43 起 /api/state 只认注册表里的单元：夹具得先登记 fee，否则 404 是对的。
	registerUnit(t, h.st, "fee")
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})
	resp, out := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，实际 %d", resp.StatusCode)
	}
	if _, ok := out["versions"]; !ok {
		t.Fatal("state 必须包含 versions")
	}
	items, ok := out["items"].(map[string]any)
	if !ok {
		t.Fatal("state 必须包含 items")
	}
	if _, ok := items["US-01"]; !ok {
		t.Fatalf("state 的 items 应包含刚写入的工作项，实际 %v", items)
	}
}

func TestStateIsNotASecondTruth(t *testing.T) {
	// /api/state 与 /api/overview 必须同源：同一个工作项在两处给出的 run/st/t 完全一致。
	h := newHarness(t)
	registerUnit(t, h.st, "fee")
	seedItem(t, h.st, store.Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})

	_, st := h.do(t, http.MethodGet, "/api/state?pbc=fee", nil, RoleBA)
	_, ov := h.do(t, http.MethodGet, "/api/overview?pbc=fee", nil, RoleBA)

	si := st["items"].(map[string]any)["US-01"].(map[string]any)
	oi := ov["items"].([]any)[0].(map[string]any)
	if si["run"] != oi["run"] || si["st"] != oi["st"] || si["t"] != oi["t"] {
		t.Fatalf("state 与 overview 不一致：state=%v overview=%v", si, oi)
	}
}

func TestHandlerServesStaticWithoutShadowingAPI(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/health 应 200，实际 %d", resp.StatusCode)
	}
	// 测试的工作目录是 internal/bff，那里没有 web/，所以静态兜底应 404——
	// 关键是不能把静态路径回落到 API 的 JSON 响应上。
	resp2, err := http.Get(h.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("测试目录下 web/ 不存在，静态兜底应 404，实际 %d", resp2.StatusCode)
	}
}

func TestHandlerServesConfiguredWebDir(t *testing.T) {
	dir := t.TempDir()
	const index = "<!doctype html><title>配置的前端入口</title>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0644); err != nil {
		t.Fatal(err)
	}
	server := NewServer(nil, nil, NewSessions([]byte("test-key")), "dapr")
	server.WebDir = dir
	handler := server.Handler()
	for _, path := range []string{"/", "/project/example"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != index {
			t.Errorf("%s 应返回配置的前端入口，实际 %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
}

// 事实落链必须推 SSE：否则页面不会自动推进（SPEC §3.4 的语义是「通知有变化」）。
// 这里直接看事件流：先 hello，再在一条事实之后收到 change。
func TestFactAppendNotifiesSSESubscribers(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/api/timeline/stream?pbc=fee", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE 的 Content-Type 应为 text/event-stream，实际 %q", ct)
	}

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	next := func(want string) {
		t.Helper()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("事件流提前关闭，没等到 %q", want)
				}
				if strings.HasPrefix(l, want) {
					return
				}
			case <-ctx.Done():
				t.Fatalf("等待 %q 超时", want)
			}
		}
	}

	next("event: hello")
	// 第一帧 hello 之后落一条事实；app 侧没有 handler 调 publish，全靠 store 回调。
	seedItem(t, h.st, store.Item{ID: "US-07", Title: "t", Kind: "Story", PBC: "fee",
		Type: "feature", Env: "test", Mode: "fast", Run: "idea", By: "BA"})
	next("event: change")
}

// 拼错的 /api 路由与用错方法的接口都要如实拒绝——不能回 200 + 首页。
// 回 HTML 会让客户端 if (resp.ok) 进成功分支再解析失败，错误离真正原因很远（#20）。
func TestUnmatchedAPIReturns404NotIndex(t *testing.T) {
	h := newHarness(t)

	resp, out := h.do(t, http.MethodGet, "/api/definitely-not-a-route", nil, RoleBA)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("拼错的 /api 路由应 404，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("应是 JSON 错误而不是 HTML，实际 Content-Type=%q", ct)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "没有对应路由") {
		t.Fatalf("错误信息要说明「接口不存在」，实际 %v", out["error"])
	}

	// 错误方法也不许落到 SPA 兜底：GET /api/health 存在，DELETE 不存在。
	resp2, _ := h.do(t, http.MethodDelete, "/api/health", nil, RoleBA)
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("DELETE /api/health 不该回 200（方法不匹配应被拒），实际 %d", resp2.StatusCode)
	}
}
