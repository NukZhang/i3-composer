package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
)

// memDapr 是最小可用的 Dapr state store 假件：支持 etag 与 first-write-wins。
// 它的存在是为了在不装 Dapr 的环境里验证 CAS 与哈希链逻辑，
// 而不是为了替代 Dapr —— 真跑起来时用的是真 sidecar。
type memDapr struct {
	mu    sync.Mutex
	data  map[string][]byte
	etags map[string]int
}

func newMem() *memDapr { return &memDapr{data: map[string][]byte{}, etags: map[string]int{}} }

func (m *memDapr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// GET /v1.0/state/{store}/{key}  —— key 可能含冒号，PathEscape 后仍是 %3A
	if r.Method == http.MethodGet {
		key := strings.TrimPrefix(r.URL.Path, "/v1.0/state/mem/")
		key = strings.ReplaceAll(key, "%3A", ":")
		b, ok := m.data[key]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("ETag", etagOf(m.etags[key]))
		_, _ = w.Write(b)
		return
	}

	if r.Method == http.MethodPost {
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
			cur := m.etags[it.Key]
			if it.Options.Concurrency == dapr.ConcurrencyFirstWrite && it.Etag != "" && it.Etag != etagOf(cur) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			m.data[it.Key] = it.Value
			m.etags[it.Key]++
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func etagOf(n int) string { return "e" + string(rune('0'+n%10)) }

func newStore(t *testing.T) *Store {
	t.Helper()
	m := newMem()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return New(dapr.New(srv.URL), "mem")
}

func TestAppendChainsHashes(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	f1, err := s.Append(ctx, "fee", FactPBCCreated, "PA", map[string]string{"name": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if f1.Seq != 1 || f1.Prev != "" {
		t.Fatalf("首条事实 seq=1 prev 为空，实际 %+v", f1)
	}
	f2, err := s.Append(ctx, "fee", FactArtifactDrafted, "x-ba", map[string]string{"v": "1.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if f2.Seq != 2 || f2.Prev != f1.Hash {
		t.Fatalf("第二条事实必须挂在前一条哈希上：prev=%q want %q", f2.Prev, f1.Hash)
	}

	fs, err := s.Load(ctx, "fee")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Seq != 1 || fs[1].Seq != 2 {
		t.Fatalf("加载应按 seq 升序返回 2 条，实际 %+v", fs)
	}
}

func TestOpIDIsIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	a, err := s.AppendOp(ctx, "fee", "inst1/0", FactItemStepDone, "i3", map[string]any{"step": 0})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AppendOp(ctx, "fee", "inst1/0", FactItemStepDone, "i3", map[string]any{"step": 0})
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash || b.Seq != a.Seq {
		t.Fatalf("同一 OpID 重放不得产生第二条事实：第一次 %+v 第二次 %+v", a, b)
	}
	fs, _ := s.Load(ctx, "fee")
	if len(fs) != 1 {
		t.Fatalf("日志应只有 1 条，实际 %d", len(fs))
	}
}

func TestConcurrentAppendsDoNotCorruptChain(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			if _, err := s.Append(ctx, "fee", FactAnnotationAdded, "BA", map[string]int{"i": i}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// 关键断言：并发写后链仍然自洽。如果按 seq 定键，这里会断链。
	fs, err := s.Load(ctx, "fee")
	if err != nil {
		t.Fatalf("并发写后日志不可用：%v", err)
	}
	if len(fs) != n {
		t.Fatalf("应落 %d 条事实，实际 %d", n, len(fs))
	}
	for i := 1; i < len(fs); i++ {
		if fs[i].Prev != fs[i-1].Hash {
			t.Fatalf("seq %d 与 %d 之间链断裂", i, i+1)
		}
	}
}

func TestLoadDetectsTampering(t *testing.T) {
	m := newMem()
	srv := httptest.NewServer(m)
	defer srv.Close()
	s := New(dapr.New(srv.URL), "mem")
	ctx := t.Context()

	if _, err := s.Append(ctx, "fee", FactPBCCreated, "PA", map[string]string{"name": "x"}); err != nil {
		t.Fatal(err)
	}
	// 篡改：直接把第一条事实的 payload 改掉，不动哈希。
	for k, v := range m.data {
		if strings.Contains(k, ":00000001:") {
			m.data[k] = []byte(strings.Replace(string(v), `"x"`, `"y"`, 1))
		}
	}
	if _, err := s.Load(ctx, "fee"); err == nil {
		t.Fatal("篡改后的日志必须加载失败")
	}
}

func TestProjectIsDeterministic(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	steps := StepPlan("test")
	it := Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee", Type: "feature",
		Env: "test", Run: "idea", Steps: steps}
	if _, err := s.Append(ctx, "fee", FactItemCreated, "BA", it); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, "fee", FactItemRunStarted, "BA", map[string]any{"id": "US-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, "fee", FactItemStepDone, "i3", map[string]any{"id": "US-01", "step": 0, "at": "11-22 15:18"}); err != nil {
		t.Fatal(err)
	}

	fs, err := s.Load(ctx, "fee")
	if err != nil {
		t.Fatal(err)
	}
	a, err := Project("fee", fs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Project("fee", fs)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("同样的日志必须投影出同样的状态")
	}
	got := a.Items["US-01"]
	if got.Run != "running" || got.Step != 1 || got.Steps[0].St != "done" {
		t.Fatalf("投影结果不符：run=%s step=%d step0=%s", got.Run, got.Step, got.Steps[0].St)
	}
}

func TestProjectCommitAcceptsTreeVersions(t *testing.T) {
	facts := []Fact{
		factOf(t, 1, FactArtifactDrafted, map[string]any{"t": "prd", "v": "1.0.1", "st": domain.StDraft}),
		factOf(t, 2, FactCommitCreated, map[string]any{
			"no": "1.0.1", "treeSha": "sha256:x",
			"tree": map[string]string{"prd": "1.0.1"},
		}),
	}
	v, err := Project("fee", facts)
	if err != nil {
		t.Fatal(err)
	}
	if vs := v.Versions[domain.PRD]; len(vs) != 1 || vs[0].St != domain.StAccepted {
		t.Fatalf("提交后树里的版本应变为 accepted，实际 %+v", vs)
	}
}

func factOf(t *testing.T, seq int, typ string, payload any) Fact {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	f := Fact{Seq: seq, Type: typ, Payload: raw}
	f.Hash = hashFact(f)
	return f
}
func TestRunStartedResetsPreviousRound(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	it := Item{ID: "US-01", Title: "t", Kind: "Story", PBC: "fee", Type: "feature",
		Env: "test", Run: "published", Tag: "v1.0.0", Version: "1.0.2",
		URL: "https://x", Steps: StepPlan("test")}
	if _, err := s.Append(ctx, "fee", FactItemCreated, "BA", it); err != nil {
		t.Fatal(err)
	}
	// 第一轮：全部完成并发布。

	if _, err := s.Append(ctx, "fee", FactItemRunStarted, "DA", map[string]any{"id": "US-01"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := s.Append(ctx, "fee", FactItemStepDone, "i3", map[string]any{"id": "US-01", "step": i, "at": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Append(ctx, "fee", FactItemPublished, "SA",
		map[string]any{"id": "US-01", "version": "1.0.2", "tag": "v1.0.0", "url": "https://x"}); err != nil {
		t.Fatal(err)
	}
	// 第二轮重新发起。
	if _, err := s.Append(ctx, "fee", FactItemRunStarted, "DA", map[string]any{"id": "US-01"}); err != nil {
		t.Fatal(err)
	}

	fs, err := s.Load(ctx, "fee")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Project("fee", fs)
	if err != nil {
		t.Fatal(err)
	}
	got := v.Items["US-01"]
	if got.Run != "running" || got.Step != 0 {
		t.Fatalf("重新发起后应为 running/step=0，实际 %s/%d", got.Run, got.Step)
	}
	// 上一轮的发布结果必须清空，否则会出现「running 但已有 tag」的矛盾投影。
	if got.Tag != "" || got.Version != "" || got.URL != "" {
		t.Fatalf("上一轮发布结果应清空，实际 tag=%q ver=%q url=%q", got.Tag, got.Version, got.URL)
	}
	for i, st := range got.Steps {
		if st.St != "todo" || st.At != "" {
			t.Fatalf("第 %d 步应为 todo 且无时间，实际 %s/%q", i, st.St, st.At)
		}
	}
}
