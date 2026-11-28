package codegen

import "strings"

// 代码模板按术语渲染（#93）。
//
// 以前这里是整段写死的缴费业务：滞纳金 0.05% / 封顶 20%、「逾期天数」、
// 两条种子记录 A-101/B-201——标题「批量生成缴费单」和标题「登记报修工单」
// 生成出来的东西一模一样。那不是按规格生成，是把模板作者的口径塞给客户。
//
// 现在留给模板的只有结构：记录、存储、处理器、主函数。业务名词（资源名、
// 字段、标题）全部来自术语，代码里不再出现任何未经确认的费率、上限或种子数据。

// codeSubs 是代码模板的占位符取值。
func codeSubs(s Spec) map[string]string {
	t := s.Terms()
	subs := map[string]string{
		"UNIT":   s.UnitName,
		"TITLE":  s.Title,
		"AT":     s.At,
		"WORD":   t.Word,
		"PLURAL": t.Plural,
		"MOD":    moduleOf(s),
		"ID_PAD": pad("ID", 9),
	}
	for i := range t.Fields {
		subs["F"+itoa(i)+"_JSON"] = t.Fields[i]
		subs["F"+itoa(i)+"_GO"] = camel(t.Fields[i])
		subs["F"+itoa(i)+"_PAD"] = pad(camel(t.Fields[i]), 9)
		subs["F"+itoa(i)+"_Z"] = t.Shown[i]
		subs["F"+itoa(i)+"_VALUE"] = sampleValue(t.Fields[i], "{{KEY}}")
	}
	subs["AMOUNT_FIELD"] = amountField(t)
	subs["AMOUNT_JSON"] = amountJSON(t)
	subs["STATS_CALL"] = statsCall(t)
	subs["STATS_LOOP"] = statsLoop(t)
	return subs
}

func amountField(t Term) string {
	if t.Amount {
		return "Amount    float64 `json:\"amount\"`"
	}
	return "// 这条业务没有金额口径：统计只给条数。\n\t_ struct{}"
}

func amountJSON(t Term) string {
	if t.Amount {
		return `"amountTotal": total`
	}
	return `"amountTotal": nil`
}

// statsCall 与 amountJSON 配套：没有金额口径时不要把 total 取出来，
// 否则生成物自己编译不过。
func statsCall(t Term) string {
	if t.Amount {
		return "n, total := st.stats()"
	}
	return "n, _ := st.stats()"
}

// statsLoop 是统计的循环体：带金额口径时累加，否则只数条数（不留死变量）。
func statsLoop(t Term) string {
	if t.Amount {
		return "for _, r := range s.records {\n\t\ttotal += r.Amount\n\t}"
	}
	return "// 这条业务没有金额口径：统计只给条数。"
}

func moduleOf(s Spec) string {
	if s.UnitID == "" {
		return "i3/unit"
	}
	return "i3/" + s.UnitID
}

// camel 把 snake_case 的 JSON 名变成导出的 Go 字段名。
func camel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func itoa(i int) string { return string(rune('0' + i)) }

// pad 补齐字段名，让生成的代码保持 gofmt 的对齐（生成物也应当像人写的）。
func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

const goModTmpl = `module {{MOD}}

go 1.21
`

const mainTmpl = `// {{UNIT}} 的交付服务：{{WORD}}。
//
// 由 X-全栈工程师生成 · {{AT}}
// 结构只有骨架：记录、进程内存储、健康检查与列表/统计/生成三个接口。
// 业务口径来自 spec 制品，代码里不写死任何费率、上限或种子数据（#93）。
package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
)

// Unit 是本单元的标识，出现在健康检查与错误里。
const Unit = "{{UNITID}}"

// Name 是本单元的名字。
const Name = "{{UNIT}}"

// Record 是一条{{WORD}}。
type Record struct {
	{{ID_PAD}} string ` + "`json:\"id\"`" + `
	{{F0_PAD}} string ` + "`json:\"{{F0_JSON}}\"`" + `
	{{F1_PAD}} string ` + "`json:\"{{F1_JSON}}\"`" + `
	{{F2_PAD}} string ` + "`json:\"{{F2_JSON}}\"`" + `
	{{AMOUNT_FIELD}}
}

type store struct {
	mu      sync.Mutex
	records []Record
	byKey   map[string][]Record // 幂等键 → 这次生成出来的记录
	seq     int
}

func newStore() *store {
	return &store{byKey: map[string][]Record{}}
}

func (s *store) list() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Record(nil), s.records...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *store) stats() (int, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0.0
	{{STATS_LOOP}}
	return len(s.records), total
}

// generate 按幂等键批量生成。同一键重复调用只入账一次。
func (s *store) generate(key, account string, count int, createdAt string) ([]Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		key = "no-key"
	}
	if got, ok := s.byKey[key]; ok {
		return got, false
	}
	if count <= 0 {
		count = 1
	}
	out := make([]Record, 0, count)
	for i := 0; i < count; i++ {
		s.seq++
		out = append(out, Record{
			ID:       strconv.Itoa(s.seq),
			{{F0_GO}}: account,
			{{F1_GO}}: "{{F1_VALUE}}",
			{{F2_GO}}: "{{F2_VALUE}}",
		})
	}
	s.records = append(s.records, out...)
	s.byKey[key] = out
	return out, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// newMux 装配路由。main 与测试共用它，所以测试打的是真的处理器。
func newMux(st *store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": Unit, "name": Name})
	})
	mux.HandleFunc("/api/{{PLURAL}}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": st.list()})
	})
	mux.HandleFunc("/api/{{PLURAL}}/stats", func(w http.ResponseWriter, r *http.Request) {
		{{STATS_CALL}}
		writeJSON(w, http.StatusOK, map[string]any{"count": n, {{AMOUNT_JSON}}})
	})
	mux.HandleFunc("/api/{{PLURAL}}/generate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key     string ` + "`json:\"key\"`" + `
			Account string ` + "`json:\"account\"`" + `
			Count   int    ` + "`json:\"count\"`" + `
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不合法"})
			return
		}
		recs, created := st.generate(in.Key, in.Account, in.Count, "{{AT}}")
		writeJSON(w, http.StatusOK, map[string]any{
			"generated": len(recs),
			"created":   created,
			"items":     recs,
		})
	})
	return mux
}

func main() {
	st := newStore()
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	// 部署流程靠这一行拿到真实端口：进程监听随机端口时，
	// 只有进程自己知道端口是多少。
	log.Printf("listen=%s", ln.Addr().String())
	log.Fatal(http.Serve(ln, newMux(st)))
}
`

const mainTestTmpl = `package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 测试只断言本单元自己的口径：健康检查、列表、幂等、统计一致。
// 业务费率、上限这类「规格里才有」的数字不进测试——它们来自 spec，不是模板。

// testMux 与 main 用同一个装配：测试打的是真处理器。
func testMux() *http.ServeMux {
	st := newStore()
	st.generate("seed-1", "A-1", 2, "seed")
	st.generate("seed-2", "B-2", 1, "seed")
	return newMux(st)
}

// TestHealthz：AC-1
func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["unit"] != Unit {
		t.Fatalf("健康检查应带单元标识，实际 %v", body["unit"])
	}
}

// TestListReadable：AC-2
func TestListReadable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}", nil)
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应 200，实际 %d", rec.Code)
	}
	var body struct {
		Items []map[string]any ` + "`json:\"items\"`" + `
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, r := range body.Items {
		if r["id"] == "" || r["id"] == nil {
			t.Fatalf("记录缺 id：%+v", r)
		}
	}
}

// TestGenerateIsIdempotent：AC-3
func TestGenerateIsIdempotent(t *testing.T) {
	// 同一个服务实例：幂等是「同一把键重复提交只入账一次」。
	mux := testMux()
	body := strings.NewReader(` + "`{\"key\":\"k1\",\"account\":\"A-1\",\"count\":3}`" + `)
	req := httptest.NewRequest(http.MethodPost, "/api/{{PLURAL}}/generate", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("生成应 200，实际 %d", rec.Code)
	}
	var first struct {
		Generated int  ` + "`json:\"generated\"`" + `
		Created   bool ` + "`json:\"created\"`" + `
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Generated != 3 || !first.Created {
		t.Fatalf("首次生成应入账 3 条，实际 %+v", first)
	}

	// 同一个幂等键再来一次：只入账一次。
	body2 := strings.NewReader(` + "`{\"key\":\"k1\",\"account\":\"A-1\",\"count\":3}`" + `)
	req2 := httptest.NewRequest(http.MethodPost, "/api/{{PLURAL}}/generate", body2)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	var second struct {
		Generated int  ` + "`json:\"generated\"`" + `
		Created   bool ` + "`json:\"created\"`" + `
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("同一幂等键重复提交不应再入账")
	}
	if second.Generated != 3 {
		t.Fatalf("重复提交应返回同一结果，实际 %d", second.Generated)
	}
}

// TestStatsMatchList：AC-4
func TestStatsMatchList(t *testing.T) {
	mux := testMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}", nil))
	var list struct {
		Items []map[string]any ` + "`json:\"items\"`" + `
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}/stats", nil))
	var stats struct {
		Count int ` + "`json:\"count\"`" + `
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Count != len(list.Items) {
		t.Fatalf("统计条数 %d 与列表 %d 不一致", stats.Count, len(list.Items))
	}
}

// TestBadBodyRejected：AC-5
func TestBadBodyRejected(t *testing.T) {
	mux := testMux()
	req := httptest.NewRequest(http.MethodPost, "/api/{{PLURAL}}/generate", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法请求体应 400，实际 %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" || body["error"] == nil {
		t.Fatalf("400 要带一句原因，实际 %+v", body)
	}
}
`
