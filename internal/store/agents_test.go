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
	v, err := ProjectBRD(facts, "")
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

// #33：TrialRun 只记最后一次试跑，而最后一次可能是被白名单拦掉的一轮（query 空、loaded 0）。
// lastLoadedTrial 必须钉在「最近一次真的载入了知识」的那一轮，不随后来的空装配滚掉。
func TestProjectAgentsLastLoadedTrialSurvivesEmptyTrial(t *testing.T) {
	loaded := domain.Trial{
		In: "你们这个热玛吉能维持多久",
		Assemblies: []domain.ContextAssembly{{
			Node:   "n3",
			Query:  "你们这个热玛吉能维持多久",
			Loaded: []domain.LoadedChunk{{Doc: "产品口径字典/热玛吉", Ver: "2026.11.1", Score: 3}},
		}},
	}
	// 第二轮：工具被白名单拦掉，kb 没拿到输入。
	empty := domain.Trial{
		In:         "你们这个热玛吉能维持多久",
		Assemblies: []domain.ContextAssembly{{Node: "n3", Query: "", Loaded: nil}},
	}
	facts := agentFacts(9, 10)
	facts = append(facts,
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": loaded, "at": "01:00", "by": "DA"})},
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": empty, "at": "02:00", "by": "DA"})},
	)
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	a := v.Agents["x-fe"]
	if a == nil {
		t.Fatal("agent 未投影出来")
	}
	if a.TrialAt != "02:00" {
		t.Fatalf("TrialRun 仍应是最新一轮，实际 at=%q", a.TrialAt)
	}
	if a.LastLoadedTrial == nil || a.LastLoadedAt != "01:00" {
		t.Fatalf("lastLoadedTrial 应停在真召回的那一轮（01:00），实际 %+v at=%q", a.LastLoadedTrial, a.LastLoadedAt)
	}
	if len(a.LastLoadedTrial.Assemblies) != 1 || a.LastLoadedTrial.Assemblies[0].Query == "" {
		t.Fatalf("lastLoadedTrial 必须带着当轮装配：%+v", a.LastLoadedTrial.Assemblies)
	}
}

// TestTrialPreciseTimeOrdersSameMinute：同一分钟内连跑两轮（先真召回、后被白名单拦掉），
// 分钟粒度的 at 分不出先后；trial.atExact 必须能把「最近一次检索」钉在真召回的那一轮（#47）。
func TestTrialPreciseTimeOrdersSameMinute(t *testing.T) {
	loaded := &domain.Trial{In: "x", Assemblies: []domain.ContextAssembly{
		{Node: "n3", Query: "退款时效", Loaded: []domain.LoadedChunk{{Doc: "a.md", Score: 9}}, Budget: domain.BudgetInfo{Total: 1}},
	}}
	empty := &domain.Trial{In: "x", Assemblies: []domain.ContextAssembly{{Node: "n3", Query: "", Budget: domain.BudgetInfo{Total: 1}}}}
	loaded.AtExact = "2026-11-27T08:35:01.000000001Z"
	empty.AtExact = "2026-11-27T08:35:30.000000002Z"
	facts := agentFacts(9, 10)
	facts = append(facts,
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": loaded, "at": "08:35", "by": "DA"})},
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": empty, "at": "08:35", "by": "DA"})},
	)
	v, err := ProjectAgents(facts)
	if err != nil {
		t.Fatal(err)
	}
	a := v.Agents["x-fe"]
	if a.TrialRunAt != empty.AtExact {
		t.Fatalf("TrialRunAt 应是最后一轮的精确时间，实际 %q", a.TrialRunAt)
	}
	if a.LastLoadedTrial == nil || a.LastLoadedTrial.AtExact != loaded.AtExact {
		t.Fatalf("lastLoadedTrial 应停在真召回的那一轮，实际 %+v", a.LastLoadedTrial)
	}
	// 被拦的一轮（08:35:30）之后没有新的召回：不能反着说「已重新载入」。
	if v.LoadedSinceTrial(a, empty.AtExact, "08:35") {
		t.Fatal("被拦一轮之后没有真召回，LoadedSinceTrial 不应为真")
	}
	// 真召回一轮（08:35:01）之后有新的召回（就是它自己之后的那轮不算，这里反向验证）：
	if !v.LoadedSinceTrial(a, loaded.AtExact, "08:35") {
		t.Fatal("真召回一轮落地后最近一次召回就是它自己，LoadedSinceTrial 应为真")
	}
}

// TestTrialWithEmptyQueryDoesNotPinLastLoaded：query 非空、却被白名单挡到一篇没进（note 归因
// 「没读到输入」）的一轮不算「最近一次检索」；真·没召回（query 有、note「没召回」）是真检索过。
func TestTrialWithEmptyQueryDoesNotPinLastLoaded(t *testing.T) {
	loaded := &domain.Trial{Assemblies: []domain.ContextAssembly{
		{Node: "n3", Query: "退款时效", Loaded: []domain.LoadedChunk{{Doc: "a.md", Score: 9}}},
	}}
	blocked := &domain.Trial{Assemblies: []domain.ContextAssembly{{
		Node: "n3", Query: "退款时效", Note: "没读到输入：这一轮 kb 节点拿到的 query 是空的，不是知识库没召回",
	}}}
	missed := &domain.Trial{Assemblies: []domain.ContextAssembly{{
		Node: "n3", Query: "退款时效", Note: "没召回：query 读到了，但没有一篇文档命中阈值，该进知识库补文档或调阈值",
	}}}
	facts := agentFacts(9, 10)
	facts = append(facts,
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": loaded, "at": "08:00"})},
		Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": blocked, "at": "08:01"})},
	)
	v, _ := ProjectAgents(facts)
	if a := v.Agents["x-fe"]; a.LastLoadedTrial == nil || a.LastLoadedAt != "08:00" {
		t.Fatalf("被挡的一轮不应改写 lastLoadedTrial，实际 at=%q", a.LastLoadedAt)
	}
	facts = append(facts, Fact{Type: FactAgentTrial, Payload: mustJSON(map[string]any{"agentId": "x-fe", "trial": missed, "at": "08:02"})})
	v, _ = ProjectAgents(facts)
	if a := v.Agents["x-fe"]; a.LastLoadedAt != "08:02" {
		t.Fatalf("真·没召回的一轮是查过了的，应更新 lastLoadedTrial，实际 at=%q", a.LastLoadedAt)
	}
}
