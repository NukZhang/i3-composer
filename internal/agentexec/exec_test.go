package agentexec

import (
	"context"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// 测试用的编排：质检员（会话 → 读会话 → 检索 → 判定 → 分支 → 人工 → 回执）。
func qcFlow() *domain.Flow {
	return &domain.Flow{
		Nodes: []domain.FlowNode{
			{ID: "n1", N: "会话进入", T: domain.NodeStart,
				Cfg: map[string]any{"vars": []any{
					[]any{"sessionId", "string", "必填", "调度器"},
					[]any{"orderId", "string", "必填", "会话元数据"},
				}}},
			{ID: "n2", N: "读会话", T: domain.NodeTool,
				Cfg: map[string]any{"tool": "session.read", "approve": "不需要（只读）"}},
			{ID: "n3", N: "检索口径", T: domain.NodeKB,
				Cfg: map[string]any{"out": "chunks[]（带出处与文档版本）"}},
			{ID: "n4", N: "合规判定", T: domain.NodeLLM,
				Cfg: map[string]any{"temp": 0, "out": "verdict{risk, hits[]}",
					"prompt": "你是质检员，逐条检查承诺疗效与绝对化用语。"}},
			{ID: "n5", N: "风险分级", T: domain.NodeBranch,
				Cfg: map[string]any{
					"pre":   "先跑 guard.check：医疗承诺 / 疗效保证 / 退费承诺 三类红线",
					"conds": []any{[]any{"risk == \"high\"", "转人工复核"}, []any{"else", "出报告入库"}},
				}},
			{ID: "n6", N: "转人工", T: domain.NodeHITL,
				Cfg: map[string]any{"who": "质检员（人工）", "timeout": "4 小时",
					"onTimeout": "升级给质检组长，不出自动结论"}},
			{ID: "n7", N: "回执入库", T: domain.NodeEnd,
				Cfg: map[string]any{"tool": "report.write"}},
		},
		Edges: [][3]string{
			{"n1", "n2", ""}, {"n2", "n3", ""}, {"n3", "n4", ""}, {"n4", "n5", ""},
			{"n5", "n6", "high"}, {"n5", "n7", "常规"}, {"n6", "n7", ""},
		},
	}
}

func qcAgent() *domain.Agent {
	return &domain.Agent{ID: "x-qc", Name: "X-质检员", Flow: qcFlow()}
}

// 命中红线的会话应判 high 并走人工分支。
func TestJudgeProducesRealVerdict(t *testing.T) {
	e := &Exec{}
	run, err := e.Execute(context.Background(), qcAgent(), map[string]any{
		"sessionId": "S-20261124-0912",
		"orderId":   "ORD-88213",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Output["risk"] != "high" {
		t.Fatalf("「年轻五岁没问题」应判 high，实际 %v", run.Output["risk"])
	}
	hits, _ := run.Output["hits"].([]any)
	if len(hits) == 0 {
		t.Fatal("应给出命中证据")
	}
	first, _ := hits[0].(map[string]any)
	if first["rule"] != "R-03" {
		t.Fatalf("应命中 R-03，实际 %v", first["rule"])
	}
	if !run.Escalated {
		t.Fatal("判 high 应转人工")
	}
	if len(run.Branch) == 0 || run.Branch[0] != "high" {
		t.Fatalf("应走 high 分支，实际 %v", run.Branch)
	}
}

// 正常会话应判 normal 且不出结论，走「出报告入库」。
func TestNormalSessionDoesNotEscalate(t *testing.T) {
	e := &Exec{}
	run, err := e.Execute(context.Background(), qcAgent(), map[string]any{
		"sessionId": "S-20261124-1010", // 「好的谢谢」
		"orderId":   "ORD-88301",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Output["risk"] != "normal" {
		t.Fatalf("正常会话应判 normal，实际 %v", run.Output["risk"])
	}
	if run.Escalated {
		t.Fatal("正常会话不该转人工")
	}
	if len(run.Branch) == 0 || run.Branch[0] != "常规" {
		t.Fatalf("应走常规分支，实际 %v", run.Branch)
	}
}

// 关键性质：输入变了，输出必须跟着变——回放做不到这一点。
func TestDifferentInputProducesDifferentOutput(t *testing.T) {
	e := &Exec{}
	bad, err := e.Execute(context.Background(), qcAgent(), map[string]any{"sessionId": "S-20261124-0912"})
	if err != nil {
		t.Fatal(err)
	}
	good, err := e.Execute(context.Background(), qcAgent(), map[string]any{"sessionId": "S-20261124-1010"})
	if err != nil {
		t.Fatal(err)
	}
	if bad.Output["risk"] == good.Output["risk"] {
		t.Fatalf("不同输入应得出不同判定：%v vs %v", bad.Output["risk"], good.Output["risk"])
	}
	if bad.Raw == good.Raw {
		t.Fatal("不同输入的输出内容不应相同（那说明是回放）")
	}
}

// 知识库检索要真命中，并且带出处。
func TestKnowledgeSearchReturnsSources(t *testing.T) {
	e := &Exec{}
	chunks := e.search("x-qc", "承诺疗效 年轻五岁", 5)
	if len(chunks) == 0 {
		t.Fatal("应检索到文档")
	}
	c := chunks[0]
	if c["lib"] == "" || c["title"] == "" {
		t.Fatalf("检索结果应带出处：%v", c)
	}
}

// 催缴：欠费 86000 应 escalate（超过 5 万转人工线）。
func TestDunEscalatesLargeAmount(t *testing.T) {
	e := &Exec{}
	flow := &domain.Flow{
		Nodes: []domain.FlowNode{
			{ID: "n1", N: "派发任务", T: domain.NodeStart,
				Cfg: map[string]any{"vars": []any{[]any{"customerId", "string", "必填", "策略引擎"}}}},
			{ID: "n2", N: "读账单", T: domain.NodeTool,
				Cfg: map[string]any{"tool": "bill.read"}},
			{ID: "n3", N: "生成话术", T: domain.NodeLLM,
				Cfg: map[string]any{"temp": 0.3, "out": "draft{text, escalate, reason}"}},
			{ID: "n4", N: "要人工吗", T: domain.NodeBranch,
				Cfg: map[string]any{"conds": []any{
					[]any{"escalate", "转人工处理"}, []any{"else", "出稿等发送"}}}},
			{ID: "n5", N: "转人工", T: domain.NodeHITL,
				Cfg: map[string]any{"who": "管家（人工）", "timeout": "2 小时"}},
			{ID: "n6", N: "出稿待发", T: domain.NodeEnd},
		},
		Edges: [][3]string{
			{"n1", "n2", ""}, {"n2", "n3", ""}, {"n3", "n4", ""},
			{"n4", "n5", "需确认"}, {"n4", "n6", "直接出稿"}, {"n5", "n6", "人处理后"},
		},
	}
	a := &domain.Agent{ID: "x-dun", Name: "X-催缴专员", Flow: flow}
	run, err := e.Execute(context.Background(), a, map[string]any{"customerId": "C-40914"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Output["escalate"] != true {
		t.Fatalf("欠费 86000 应升级转人工，实际 %v", run.Output["escalate"])
	}
	if !run.Escalated {
		t.Fatal("应转人工")
	}
	draft, _ := run.Output["draft"].(string)
	if draft == "" {
		t.Fatal("应生成话术草稿")
	}
}

// 编排不合法时要拒绝执行，而不是跑出一堆假结果。
func TestExecuteRejectsInvalidFlow(t *testing.T) {
	e := &Exec{}
	bad := &domain.Agent{ID: "x", Flow: &domain.Flow{
		Nodes: []domain.FlowNode{{ID: "n1", N: "只有结束", T: domain.NodeEnd}},
	}}
	if _, err := e.Execute(context.Background(), bad, nil); err == nil {
		t.Fatal("没有开始节点的编排必须被拒绝")
	}
}

// `.length > 0` 这种写法必须真的能用。
//
// 早先 evalCond 先按 `>` 拆成 "hits.length" 与 "0"，再去上游找一个
// 名叫 "hits.length" 的字段——永远找不到，于是这一半条件永远为假，
// 需要转人工的对话被判成不用转。真实编排里就有
// "escalate || hits.length > 0" 这样的条件。
func TestLengthConditionIsEvaluated(t *testing.T) {
	e := &Exec{}
	outs := map[string]map[string]any{
		"n4": {"hits": []any{map[string]any{"rule": "R-03"}, map[string]any{"rule": "R-05"}},
			"escalate": false},
	}
	// 只有 hits 命中综述时，右侧这半边要顶得住。
	if !e.evalCond("hits.length > 0", outs) {
		t.Fatal("hits 有 2 条，hits.length > 0 应为真")
	}
	if !e.evalCond("escalate || hits.length > 0", outs) {
		t.Fatal("escalate 为假但 hits 非空，整体应为真")
	}
	// 没有命中时不能误判。
	if e.evalCond("hits.length > 0", map[string]map[string]any{"n4": {"hits": []any{}}}) {
		t.Fatal("hits 为空，hits.length > 0 应为假")
	}
	// 字段比较照常。
	outs2 := map[string]map[string]any{"n4": {"risk": "high"}}
	if !e.evalCond(`risk == "high"`, outs2) {
		t.Fatal(`risk == "high" 应为真`)
	}
	if e.evalCond(`risk == "low"`, outs2) {
		t.Fatal(`risk == "low" 应为假`)
	}
}

// 同样的输入必须得到同样的结论：上游有多个节点时，
// 取同名字段不能靠 map 的随机迭代顺序。
func TestLookupIsDeterministic(t *testing.T) {
	e := &Exec{}
	outs := map[string]map[string]any{
		"n9": {"flag": true},
		"n2": {"flag": false},
	}
	for i := 0; i < 50; i++ {
		if got := e.lookup("flag", outs); got != false {
			t.Fatalf("应取节点 id 最小的那个输出（n2=false），第 %d 次得到 %v", i, got)
		}
	}
}
