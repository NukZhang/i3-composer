package codegen

// 这一文件是生成的模板。
//
// 注意：模板里所有 Go 代码都是字面量，占位符只用 {{KEY}}。
// 不用 fmt.Sprintf 是因为生成的代码里有 %（取模）与 {}（JSON），
// 交给 Sprintf 会被当成格式符吃掉。

const prdTmpl = `# {{UNIT}} · {{TITLE}}

> {{UNIT}} · 由 X-业务分析员生成 · {{AT}}

## 1. 背景
{{UNIT}} 当前由「{{UNIT}}」承载。本次变更类型：{{GOAL}}。

## 2. 目标
- {{TITLE}}
- 变更后仍要满足既有的账期、幂等与滞纳金口径。

## 3. 范围
### 3.1 本次包含
- {{TITLE}}

### 3.2 本次不包含
- 渠道接入、计费策略、跨单元数据打通。

## 4. 验收口径
见 AT 制品：{{UNIT}} 的每条 Given/When/Then 都要能被真跑一遍。

## 5. 约束
- 私有化部署，不依赖外部 SaaS。
- 一次提交一个版本号：全落或全不落。
`

const protoTmpl = `# {{UNIT}} · {{TITLE}} · 原型说明

> 由 X-UI设计师生成 · {{AT}}

## 1. 这块交付物是什么
{{TITLE}} 的界面形态：用户看到哪几屏、每屏能做什么。

## 2. 页面清单
| 屏 | 用户在这里做什么 | 关键约束 |
|---|---|---|
| 列表 | 看自己的记录、按条件筛选 | 筛选后要能二次确认 |
| 明细 | 看单条记录的字段 | 金额与逾期天数必须可读 |
| 生成 | 批量生成并确认 | 重复点击不重复入账（幂等键） |

## 3. 交互约束
- 可点击、可筛选、可翻页；状态变化要能看见。
- 空态与错误态都要有引导文案，不允许一片空白。

## 4. 验收
与 AT 一致：每条 Given/When/Then 都能在界面上真按一遍。
`

const adTmpl = `# {{UNIT}} · {{TITLE}} · 技术设计

> 由 X-全栈工程师生成 · {{AT}}

## 1. 结构
单库 + 领域聚合：{{UNIT}} 的读模型与写模型同库，靠账期与幂等键分区。

## 2. 关键设计
### 2.1 幂等
所有写接口接受幂等键：同一键重复提交只入账一次。这是「批量生成」与
「支付回调」两条验收标准的共同前提。

### 2.2 滞纳金
按日 0.05% 计，上限为本金 20%。上限在代码里写死并被测试覆盖，
不允许靠配置漂移。

### 2.3 健康检查
服务暴露 ` + "`GET /healthz`" + `，返回本单元标识与版本；部署流程以它为准入条件。

## 3. 接口
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | ` + "`/healthz`" + ` | 健康检查 |
| GET | ` + "`/api/{{PLURAL}}`" + ` | 列表 |
| POST | ` + "`/api/{{PLURAL}}/generate`" + ` | 批量生成（幂等） |
| GET | ` + "`/api/{{PLURAL}}/stats`" + ` | 统计 |

## 4. 不做什么
- 不引入外部依赖：不装包、不连外网，构建产物可离线复算。
`

const atTmpl = `# {{UNIT}} · {{TITLE}} · 验收标准

> 由 X-测试工程师生成 · {{AT}}

每条都是 Given/When/Then。测试步骤由 X-测试工程师落到代码制品的单元测试里，
构建阶段真跑一遍——不过就是不过。

{{AC}}`

const backlogTmpl = `# {{UNIT}} · {{TITLE}} · Backlog

> 由 X-业务分析员生成 · {{AT}}

## Story

- [ ] US-{{ID}} {{TITLE}}

## Task

`

const goModTmpl = `module {{MOD}}

go 1.21
`

const mainTmpl = `// {{UNIT}} 的交付服务。
//
// 由 X-全栈工程师生成 · {{AT}}
// 行为与 AT 制品一一对应：幂等、滞纳金封顶、统计口径一致。
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

// Record 是一条业务记录。
type Record struct {
	ID      string  ` + "`json:\"id\"`" + `
	Account string  ` + "`json:\"account\"`" + `
	Amount  float64 ` + "`json:\"amount\"`" + `
	Overdue int     ` + "`json:\"overdueDays\"`" + `
	Created string  ` + "`json:\"createdAt\"`" + `
}

// lateFee 是滞纳金：按日 0.05%，上限为本金 20%。
// 上限写死在这里，并由测试覆盖——不允许靠配置漂移。
func lateFee(amount float64, overdueDays int) float64 {
	if overdueDays <= 0 || amount <= 0 {
		return 0
	}
	fee := amount * 0.0005 * float64(overdueDays)
	cap := amount * 0.20
	if fee > cap {
		return cap
	}
	return fee
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
	for _, r := range s.records {
		total += r.Amount
	}
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
			ID:      strconv.Itoa(s.seq),
			Account: account,
			Amount:  1000 + float64(s.seq*37%900),
			Overdue: s.seq % 60,
			Created: createdAt,
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
		n, total := st.stats()
		writeJSON(w, http.StatusOK, map[string]any{"count": n, "amountTotal": total})
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
	// 预置两条记录，让列表与统计接口一开始就有内容。
	st.generate("seed-1", "A-101", 2, "{{AT}}")
	st.generate("seed-2", "B-201", 1, "{{AT}}")

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

// testMux 与 main 用同一个装配：测试打的是真处理器。
func testMux() *http.ServeMux {
	st := newStore()
	st.generate("seed-1", "A-101", 2, "seed")
	st.generate("seed-2", "B-201", 1, "seed")
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

// TestListNotEmpty：AC-2
func TestListNotEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}", nil)
	rec := httptest.NewRecorder()
	testMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应 200，实际 %d", rec.Code)
	}
	var body struct {
		Items []Record ` + "`json:\"items\"`" + `
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 {
		t.Fatal("列表不应为空")
	}
	for _, r := range body.Items {
		if r.ID == "" || r.Amount <= 0 {
			t.Fatalf("记录字段不完整：%+v", r)
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

// TestLateFeeCap：AC-4
func TestLateFeeCap(t *testing.T) {
	// 按日 0.05%：逾期 400 天恰好到本金 20% 的封顶线。
	fee := lateFee(10000, 400)
	if fee > 2000 {
		t.Fatalf("滞纳金不得超过本金 20%%，实际 %v", fee)
	}
	if fee != 2000 {
		t.Fatalf("逾期 400 天、金额 10000 的滞纳金应为封顶值 2000，实际 %v", fee)
	}
	// 逾期更久也不增加：封顶是硬的。
	if over := lateFee(10000, 900); over != 2000 {
		t.Fatalf("逾期 900 天仍应封顶 2000，实际 %v", over)
	}
	if fee2 := lateFee(10000, 1); fee2 != 5 {
		t.Fatalf("逾期 1 天应按 0.05%% 计，即 5，实际 %v", fee2)
	}
	if fee3 := lateFee(10000, 60); fee3 != 300 {
		t.Fatalf("逾期 60 天应按 0.05%%/日 计，即 300，实际 %v", fee3)
	}
}

// TestStatsMatchList：AC-5
func TestStatsMatchList(t *testing.T) {
	mux := testMux()
	req := httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var list struct {
		Items []Record ` + "`json:\"items\"`" + `
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/{{PLURAL}}/stats", nil))
	var stats struct {
		Count       int     ` + "`json:\"count\"`" + `
		AmountTotal float64 ` + "`json:\"amountTotal\"`" + `
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Count != len(list.Items) {
		t.Fatalf("统计条数 %d 与列表 %d 不一致", stats.Count, len(list.Items))
	}
	var sum float64
	for _, r := range list.Items {
		sum += r.Amount
	}
	if stats.AmountTotal != sum {
		t.Fatalf("统计金额 %v 与列表合计 %v 不一致", stats.AmountTotal, sum)
	}
}
`
