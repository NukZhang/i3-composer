package store

import (
	"encoding/json"
	"testing"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// 交付单元构成一旦落账就不许改：同一 id 再落 unit.created 只在校验意义上覆盖。
func TestProjectUnitsKeepsOrderAndKind(t *testing.T) {
	facts := []Fact{
		{Type: FactUnitCreated, Payload: []byte(`{"id":"fee","name":"收费","code":"PBC-FEE","kind":"pbc"}`)},
		{Type: FactUnitCreated, Payload: []byte(`{"id":"dun","name":"催缴","code":"X","kind":"hybrid","agentId":"x-dun"}`)},
	}
	v, err := ProjectUnits(facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Units) != 2 || v.Units[0].ID != "fee" || v.Units[1].Kind != "hybrid" {
		t.Fatalf("单元投影不对：%+v", v.Units)
	}
	if v.Units[1].AgentID != "x-dun" {
		t.Fatalf("组合单元必须带 agentId：%+v", v.Units[1])
	}
}

func TestProjectUnitsRejectsMissingID(t *testing.T) {
	_, err := ProjectUnits([]Fact{{Type: FactUnitCreated, Payload: []byte(`{"kind":"pbc"}`)}})
	if err == nil {
		t.Fatal("缺 id 必须报错")
	}
}

func agentFacts(pass, total int) []Fact {
	a := domain.Agent{
		ID: "x-fe", Name: "X-全栈", Ver: "1.0.0", Model: "m",
		Stages: []string{"done", "done", "done", "done", "todo"},
		Tools:  []string{"repo.read"},
		Prompt: "p",
		Eval:   [2]int{8, 10},
	}
	return []Fact{
		{Type: FactAgentDefined, Payload: mustJSON(a)},
		{Type: FactAgentEvalRun, Payload: mustJSON(map[string]any{
			"agentId": "x-fe",
			"run":     domain.EvalRun{ID: "exp-1", Ver: "1.0.1", Pass: pass, Total: total},
		})},
	}
}

func TestProjectAgentsEvalRunMovesRateAndKeepsNewestFirst(t *testing.T) {
	v, err := ProjectAgents(agentFacts(9, 10))
	if err != nil {
		t.Fatal(err)
	}
	a := v.Agents["x-fe"]
	if a == nil {
		t.Fatal("agent 未投影出来")
	}
	if a.Eval != [2]int{9, 10} {
		t.Fatalf("评测结果应覆盖定义里的值，实际 %v", a.Eval)
	}
	if a.Ver != "1.0.1" {
		t.Fatalf("运行版本应回写，实际 %q", a.Ver)
	}
	if len(a.Runs) != 1 || a.Runs[0].ID != "exp-1" {
		t.Fatalf("运行历史不对：%+v", a.Runs)
	}
}

// 基线只有一条：设新的就是换掉旧的。
func TestProjectAgentsBaselineIsExclusive(t *testing.T) {
	facts := agentFacts(9, 10)
	facts = append(facts,
		Fact{Type: FactAgentEvalRun, Payload: mustJSON(map[string]any{
			"agentId": "x-fe", "run": domain.EvalRun{ID: "exp-2", Ver: "1.0.2", Pass: 10, Total: 10},
		})},
		Fact{Type: FactAgentBaseline, Payload: mustJSON(map[string]any{"agentId": "x-fe", "runId": "exp-1"})},
		Fact{Type: FactAgentBaseline, Payload: mustJSON(map[string]any{"agentId": "x-fe", "runId": "exp-2"})},
	)
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	bases := 0
	for _, r := range v.Agents["x-fe"].Runs {
		if r.Base {
			bases++
			if r.ID != "exp-2" {
				t.Fatalf("基线应是最新设的那条，实际 %s", r.ID)
			}
		}
	}
	if bases != 1 {
		t.Fatalf("基线必须恰好一条，实际 %d", bases)
	}
}

func TestProjectAgentsPublishStartsAtTenPercent(t *testing.T) {
	facts := agentFacts(10, 10)
	facts = append(facts, Fact{Type: FactAgentPublish, Payload: mustJSON(map[string]any{
		"agentId": "x-fe", "ver": "1.0.1", "digest": "sha256:abc",
	})})
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	a := v.Agents["x-fe"]
	if !a.Published || a.Rollout != 10 {
		t.Fatalf("发布后应进入 10%% 档，实际 published=%v rollout=%d", a.Published, a.Rollout)
	}
}

func TestProjectAgentsRollbackZeroesTraffic(t *testing.T) {
	facts := agentFacts(10, 10)
	facts = append(facts,
		Fact{Type: FactAgentPublish, Payload: mustJSON(map[string]any{"agentId": "x-fe", "ver": "1.0.1"})},
		Fact{Type: FactAgentRollout, Payload: mustJSON(map[string]any{"agentId": "x-fe", "pct": 50})},
		Fact{Type: FactAgentRollback, Payload: mustJSON(map[string]any{"agentId": "x-fe", "to": "1.0.0"})},
	)
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	a := v.Agents["x-fe"]
	if a.Rollout != 0 || a.RollbackTo != "1.0.0" {
		t.Fatalf("回滚后流量应归零，实际 rollout=%d to=%q", a.Rollout, a.RollbackTo)
	}
}

func TestProjectAgentsIgnoresIllegalRollout(t *testing.T) {
	facts := agentFacts(10, 10)
	facts = append(facts,
		Fact{Type: FactAgentPublish, Payload: mustJSON(map[string]any{"agentId": "x-fe", "ver": "1.0.1"})},
		Fact{Type: FactAgentRollout, Payload: mustJSON(map[string]any{"agentId": "x-fe", "pct": 37})},
	)
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Agents["x-fe"].Rollout; got != 10 {
		t.Fatalf("非 10/50/100 的档位不得生效，实际 %d", got)
	}
}

func TestProjectBRDNewDraftSupersedesOld(t *testing.T) {
	facts := []Fact{
		{Type: FactBRDSaved, Payload: []byte(`{"v":"v1.0","at":"a","by":"BA","st":"accepted","text":"# x"}`)},
		{Type: FactBRDSaved, Payload: []byte(`{"v":"v1.1","at":"b","by":"BA","st":"draft","text":"# y","note":"补 §3"}`)},
		{Type: FactBRDSaved, Payload: []byte(`{"v":"v1.2","at":"c","by":"BA","st":"draft","text":"# z"}`)},
	}
	v, err := ProjectBRD(facts)
	if err != nil {
		t.Fatal(err)
	}
	if v.V != "v1.2" || v.Text != "# z" {
		t.Fatalf("最新版不对：%+v", v)
	}
	if v.Vers[1].St != "superseded" {
		t.Fatalf("旧草稿应被取代，实际 %q", v.Vers[1].St)
	}
	if v.Vers[0].St != "accepted" {
		t.Fatalf("已接受的版本不许被改，实际 %q", v.Vers[0].St)
	}
	if v.Words != 3 {
		t.Fatalf("字数统计不对：%d", v.Words)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
