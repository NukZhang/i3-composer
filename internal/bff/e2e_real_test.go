package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 真链路端到端：从空账本走到发布。
//
// 这条测试要真的连 Dapr：真的跑 workflow、真的编译代码、真的起进程、
// 真的做健康检查。所以它要一套指向真实 Dapr 的环境变量：
//
//	E2E_BASE=http://127.0.0.1:8093   跑着的隔离实例（账本前缀 e2e/、workflow i3-delivery-e2e）
//	DAPR_HTTP_ENDPOINT=http://127.0.0.1:3500
//	LEDGER_PREFIX=e2e/               与那个实例同一个账本前缀
//
// 缺任何一个就跳过——它不是「没有 Dapr 也能跑」的单元测试。
func TestRealChainEndToEnd(t *testing.T) {
	base := strings.TrimRight(os.Getenv("E2E_BASE"), "/")
	httpEP := os.Getenv("DAPR_HTTP_ENDPOINT")
	prefix := os.Getenv("LEDGER_PREFIX")
	if base == "" || httpEP == "" || prefix == "" {
		t.Skip("需要真实 Dapr 与一个隔离实例：设置 E2E_BASE / DAPR_HTTP_ENDPOINT / LEDGER_PREFIX")
	}

	// 直接读账本：事实里的 payload 是断言的来源（真实测试条数、真实 digest、真实地址）。
	log := store.New(dapr.New(httpEP), "statestore", prefix)

	post := func(path string, payload any, r Role) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range testCookies(r) {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}
	get := func(path string, r Role) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for _, c := range testCookies(r) {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	// ---- 1. 建单元 ----
	unitID := "e2e" + fmt.Sprint(time.Now().UnixNano()%100000)
	code, out := post("/api/units", map[string]any{
		"id": unitID, "name": "端到端验收单元", "code": "PBC-E2E", "kind": "pbc",
	}, RolePA)
	if code != http.StatusCreated {
		t.Fatalf("建单元应 201，实际 %d %v", code, out)
	}

	// ---- 2. 建任务 ----
	title := "端到端验收-" + fmt.Sprint(time.Now().UnixNano())
	code, out = post("/api/tasks?pbc="+unitID, map[string]any{
		"title": title, "type": "feature", "env": "test", "mode": "fast",
	}, RoleBA)
	if code != http.StatusCreated {
		t.Fatalf("建任务应 201，实际 %d %v", code, out)
	}
	itemID, _ := out["id"].(string)
	if itemID == "" {
		t.Fatalf("任务应有编号：%v", out)
	}

	// ---- 3. DA 发起（真实 workflow） ----
	code, out = post("/api/items/"+itemID+"/run?pbc="+unitID, nil, RoleDA)
	if code != http.StatusAccepted {
		t.Fatalf("发起应 202，实际 %d %v", code, out)
	}

	// ---- 4. 等它走到发布闸 ----
	var waiting map[string]any
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		_, st := get("/api/state?pbc="+unitID, RoleBA)
		if items, ok := st["items"].(map[string]any); ok {
			if it, ok := items[itemID].(map[string]any); ok && it["run"] == "waiting" {
				waiting = it
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if waiting == nil {
		t.Fatal("workflow 没走到发布闸")
	}
	t.Logf("走到发布闸：run=%v step=%v", waiting["run"], waiting["step"])

	// ---- 5. 制品里有真内容（能编译） ----
	_, st := get("/api/state?pbc="+unitID, RoleBA)
	vers, _ := st["versions"].(map[string]any)
	codeVers, _ := vers["code"].([]any)
	if len(codeVers) == 0 {
		t.Fatal("没有代码制品")
	}
	last := codeVers[len(codeVers)-1].(map[string]any)
	files, _ := last["files"].(map[string]any)
	for _, need := range []string{"go.mod", "main.go", "main_test.go"} {
		if _, ok := files[need]; !ok {
			t.Fatalf("代码制品缺 %s，实际有 %v", need, keysOf(files))
		}
	}

	// ---- 6. 测试结论是真跑出来的 ----
	facts := loadFacts(t, log, unitID)
	testFact := findFact(facts, store.FactTestRun)
	if testFact == nil {
		t.Fatal("账本里没有 test.run 事实")
	}
	tr := struct {
		Pass  int `json:"pass"`
		Total int `json:"total"`
	}{}
	b, _ := json.Marshal(testFact.Payload)
	_ = json.Unmarshal(b, &tr)
	if tr.Total == 0 {
		t.Fatalf("测试结果应有条数：%v", tr)
	}
	t.Logf("真实测试结果：%d/%d 通过", tr.Pass, tr.Total)

	// ---- 7. digest 是构建产物的内容摘要 ----
	buildFact := findFact(facts, store.FactBuildSucceeded)
	if buildFact == nil {
		t.Fatal("账本里没有 build.succeeded 事实")
	}
	digest := payloadString(t, buildFact.Payload, "digest")
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("digest 应是内容摘要，实际 %q", digest)
	}
	t.Logf("构建 digest：%s", digest)

	// ---- 8. 部署地址真的活着 ----
	deployFact := findFact(facts, store.FactDeploySucceeded)
	if deployFact == nil {
		t.Fatal("账本里没有 deploy.succeeded 事实")
	}
	url := payloadString(t, deployFact.Payload, "url")
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("部署地址应是本机进程：%q", url)
	}
	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatalf("部署地址应可访问：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", resp.StatusCode)
	}
	t.Logf("部署地址 %s 健康检查 200", url)

	// ---- 9. 发布 ----
	code, out = post("/api/items/"+itemID+"/answer?pbc="+unitID,
		map[string]any{"approve": true}, RoleSA)
	if code != http.StatusAccepted {
		t.Fatalf("拍板应 202，实际 %d %v", code, out)
	}
	published := false
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_, st := get("/api/state?pbc="+unitID, RoleBA)
		if items, ok := st["items"].(map[string]any); ok {
			if it, ok := items[itemID].(map[string]any); ok && it["run"] == "published" {
				published = true
				break
			}
		}
		time.Sleep(time.Second)
	}
	if !published {
		t.Fatal("发布未完成")
	}
	t.Log("发布完成")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// testCookies 造一个角色会话的 cookie。
func testCookies(r Role) []*http.Cookie {
	rec := httptest.NewRecorder()
	NewSessions([]byte("test-key")).Issue(rec, r)
	return rec.Result().Cookies()
}

// loadFacts 从账本读原始事实。
func loadFacts(t *testing.T, log *store.Store, pbc string) []store.Fact {
	t.Helper()
	facts, err := log.Load(context.Background(), pbc)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// findFact 找第一条某类型的事实。
func findFact(facts []store.Fact, typ string) *store.Fact {
	for i := range facts {
		if facts[i].Type == typ {
			return &facts[i]
		}
	}
	return nil
}

// payloadString 从事实的 payload 里取一个字符串字段。
func payloadString(t *testing.T, raw json.RawMessage, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload 解析失败：%v", err)
	}
	s, _ := m[key].(string)
	return s
}
