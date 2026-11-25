package bff

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 有编排的 Agent 试跑：必须真跑一遍，输出随输入变。
func TestTrialExecutesFlowInsteadOfReplayingSample(t *testing.T) {
	h := newHarness(t)
	a := goodAgent()
	a.ID = "x-qc"
	a.Flow = qcFlow()
	seedAgent(t, h.st, a, 48, 50)

	// 一条含红线的会话。
	resp, out := h.do(t, http.MethodPost, "/api/agents/x-qc/trial",
		map[string]any{"vars": map[string]any{"sessionId": "S-20261124-0912"}}, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("试跑应 202，实际 %d %v", resp.StatusCode, out)
	}
	if out["source"] != "executor" {
		t.Fatalf("有编排时应由执行器真跑，实际 source=%v", out["source"])
	}
	trial := out["trial"].(map[string]any)
	raw, _ := trial["raw"].(string)
	if !strings.Contains(raw, "high") {
		t.Fatalf("含「年轻五岁」的会话应判 high，实际 %v", raw)
	}
	nodes, _ := trial["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("应记录逐节点执行结果")
	}
	first := nodes[0].(map[string]any)
	if first["ms"].(float64) <= 0 {
		t.Fatalf("节点应有实测耗时：%v", first)
	}

	// 另一条正常会话：结论必须不同（回放做不到这件事）。
	resp, out = h.do(t, http.MethodPost, "/api/agents/x-qc/trial",
		map[string]any{"vars": map[string]any{"sessionId": "S-20261124-1010"}}, RoleDA)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("试跑应 202，实际 %d", resp.StatusCode)
	}
	trial2 := out["trial"].(map[string]any)
	raw2, _ := trial2["raw"].(string)
	if raw == raw2 {
		t.Fatal("不同输入的输出不应相同——那说明是回放，不是执行")
	}
}

// 对话验收：回复来自这一轮执行，不是评测用例里的样例。
func TestVerifyChatRunsFlow(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	a := goodAgent()
	a.ID = "x-qc"
	a.Flow = qcFlow()
	seedAgent(t, h.st, a, 48, 50)
	if err := h.st.Ensure(ctx, unitsLog); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.Append(ctx, unitsLog, store.FactUnitCreated, "PA", map[string]any{
		"id": "qa", "name": "质检", "kind": "agent", "agentId": "x-qc",
	}); err != nil {
		t.Fatal(err)
	}
	resp, out := h.do(t, http.MethodPost, "/api/units/qa/chat",
		map[string]any{"text": "你们这个热玛吉能维持多久"}, RoleBA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("对话应 201，实际 %d %v", resp.StatusCode, out)
	}
	if out["source"] != "executor" {
		t.Fatalf("有编排时回复应由执行器产出，实际 source=%v", out["source"])
	}
	reply, _ := out["reply"].(string)
	if reply == "" {
		t.Fatal("应有回复")
	}
	// 对话事实要落账。
	_, st := h.do(t, http.MethodGet, "/api/state?pbc=qa", nil, RoleBA)
	chats, _ := st["chats"].([]any)
	if len(chats) != 2 {
		t.Fatalf("一问一答应两条事实，实际 %d", len(chats))
	}
}

// 事件收发闭环：收端把事件落成事实。
func TestEventSubscriptionAndReceive(t *testing.T) {
	h := newHarness(t)
	// 订阅声明（返回的是数组，直接取原文）。
	resp, err := http.Get(h.srv.URL + "/dapr/subscribe")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var subs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		t.Fatalf("订阅声明应是 JSON 数组：%v", err)
	}
	if len(subs) == 0 {
		t.Fatal("应至少订阅一个主题")
	}
	if subs[0]["pubsubname"] != "pubsub" || subs[0]["route"] == "" {
		t.Fatalf("订阅声明不对：%v", subs[0])
	}

	// 投递一个事件。
	resp2, out := h.do(t, http.MethodPost, "/api/events/tag.created", map[string]any{
		"id": "evt-1", "source": "i3-composer", "type": "com.dapr.event.sent",
		"data": map[string]any{"pbc": "fee", "tag": "v1.0.1"},
	}, RoleBA)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("事件投递应 200，实际 %d %v", resp2.StatusCode, out)
	}
	// 收到的事件应落成事实，可查。
	_, ev := h.do(t, http.MethodGet, "/api/events", nil, RoleBA)
	list, _ := ev["events"].([]any)
	if len(list) != 1 {
		t.Fatalf("应有 1 条已收事件，实际 %d", len(list))
	}
	e := list[0].(map[string]any)
	p := e["payload"].(map[string]any)
	if p["topic"] != "tag.created" {
		t.Fatalf("事件主题不对：%v", p)
	}
}

// qcFlow 是一张最小的可执行编排（质检员链路）。
func qcFlow() *domain.Flow {
	return &domain.Flow{
		Nodes: []domain.FlowNode{
			{ID: "n1", N: "会话进入", T: domain.NodeStart,
				Cfg: map[string]any{"vars": []any{[]any{"sessionId", "string", "必填", "调度器"}}}},
			{ID: "n2", N: "读会话", T: domain.NodeTool,
				Cfg: map[string]any{"tool": "session.read"}},
			{ID: "n3", N: "合规判定", T: domain.NodeLLM,
				Cfg: map[string]any{"temp": 0, "out": "verdict{risk, hits[]}"}},
			{ID: "n4", N: "风险分级", T: domain.NodeBranch,
				Cfg: map[string]any{"conds": []any{
					[]any{"risk == \"high\"", "转人工复核"},
					[]any{"else", "出报告入库"}}}},
			{ID: "n5", N: "转人工", T: domain.NodeHITL,
				Cfg: map[string]any{"who": "质检员（人工）", "timeout": "4 小时"}},
			{ID: "n6", N: "回执入库", T: domain.NodeEnd},
		},
		Edges: [][3]string{
			{"n1", "n2", ""}, {"n2", "n3", ""}, {"n3", "n4", ""},
			{"n4", "n5", "high"}, {"n4", "n6", "常规"}, {"n5", "n6", ""},
		},
	}
}
