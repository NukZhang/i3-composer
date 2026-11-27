package bff

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/agentexec"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 本文件是原型 v2.4—v2.6 的接口：
//
//   - Agent 编排工作台：保存流程图、构建（换 digest）、试跑、沉淀为评测用例
//   - 知识库问题单派发
//   - 真人验收台：AC 判定、提 bug、验收对话
//
// 所有写操作都只落事实；闸门与判定只在 domain 里算。

// ---------- Agent 编排 ----------

// agentFlow 保存一张编排。编排进定义，digest 立即变化；
// 但 BuiltDigest 仍是上一次构建的那份——「改了没构建」在闸门里看得见。
func (s *Server) agentFlow(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBuild); err != nil {
		return err
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	var in struct {
		Flow *domain.Flow `json:"flow"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if in.Flow == nil {
		return fmt.Errorf("%w：缺少 flow", errBadRequest)
	}
	if err := in.Flow.Valid(); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentFlowSaved, c.Role.String(), map[string]any{
		"agentId": a.ID, "flow": in.Flow,
	}); err != nil {
		return err
	}
	a.Flow = in.Flow
	a.EnsureDigest()
	s.publish(agentsLog, "agent.flow.saved", map[string]any{"id": a.ID})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": a.ID, "digest": a.Digest, "builtDigest": a.BuiltDigest, "dirty": a.BuiltDigest != a.Digest,
	})
	return nil
}

// agentBuild 把当前编排序列化成一份定义：算 digest、换版本号。
//
// 没换 digest 就不是新版本——改节点、改提示词、改工具、换库版本都会换；
// 什么都没改就返回 changed=false，不制造空版本。
func (s *Server) agentBuild(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBuild); err != nil {
		return err
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	if a.Flow == nil {
		return fmt.Errorf("%w：还没有编排，先画流程图", errBadRequest)
	}
	if err := a.Flow.Valid(); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	a.EnsureDigest()
	if a.BuiltDigest == a.Digest {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": a.ID, "ver": a.Ver, "digest": a.Digest, "changed": false,
		})
		return nil
	}
	ver, err := domain.NextVersion(a.Ver)
	if err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentBuilt, c.Role.String(), map[string]any{
		"agentId": a.ID, "ver": ver, "digest": a.Digest,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.built", map[string]any{"id": a.ID, "ver": ver, "digest": a.Digest})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": a.ID, "ver": ver, "digest": a.Digest, "changed": true,
	})
	return nil
}

// agentTrial 记录一次试跑。
//
// 执行器可以提交真实结果（raw/out）；没有执行器时回放定义里存的样例，
// 并在事实里标注 source=sample——不假装模型真的被调用了。
func (s *Server) agentTrial(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBuild); err != nil {
		return err
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	var in struct {
		In    string         `json:"in"`
		Vars  map[string]any `json:"vars"`
		Raw   string         `json:"raw"`
		Out   []string       `json:"out"`
		Reply string         `json:"reply"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in)

	trial := domain.Trial{}
	out := in.Out
	raw := in.Raw
	source := "submitted"

	// 有编排就真跑一遍：输入变了，结论必须跟着变。
	if s.Exec != nil && a.Flow != nil {
		vars := map[string]any{}
		for k, v := range in.Vars {
			vars[k] = v
		}
		if in.In != "" {
			vars["in"] = in.In
		}
		run, err := s.Exec.Execute(r.Context(), a, vars)
		if err != nil {
			return fmt.Errorf("%w：试跑失败：%v", errBadRequest, err)
		}
		trial.In = in.In
		if trial.In == "" {
			trial.In = describeVars(run.Input)
		}
		trial.Raw = run.Raw
		trial.Out = summarizeRun(run)
		trial.Branch = run.Branch
		trial.Escalated = run.Escalated
		trial.Source = "executor"
		trial.Assemblies = run.Assemblies
		for _, n := range run.Nodes {
			trial.Nodes = append(trial.Nodes, domain.TrialNode{
				ID: n.ID, Name: n.Name, Type: n.Type, Ms: n.Ms,
				Out: oneLine(n.Out), Wait: n.Wait, Blocked: n.Notes,
			})
		}
	} else {
		// 没有编排（Agent 未定义流程）：只能由外部执行器提交结果。
		if a.Trial == nil && in.Raw == "" {
			return fmt.Errorf("%w：这个 Agent 没有编排，也没有可用的样例；真实结果需由执行器提交", errBadRequest)
		}
		if a.Trial != nil {
			trial.In = a.Trial.In
			if raw == "" {
				raw, out, source = a.Trial.Raw, a.Trial.Out, "sample"
			}
		}
		if in.In != "" {
			trial.In = in.In
		}
		trial.Raw = raw
		if out != nil {
			trial.Out = out
		} else if a.Trial != nil {
			trial.Out = a.Trial.Out
		}
		trial.Source = source
	}
	at := time.Now().Format("01-02 15:04")
	// 装配事实先于试跑结论落账：先记「这一轮看到了什么」，再记「这一轮得出了什么」。
	// 反过来的话，中途崩了就会出现「有结论、不知道它读的是哪一版库」——
	// 这正是评测失败没法归因到「没召回」的原因。
	for _, asm := range trial.Assemblies {
		if _, err := s.Log.Append(r.Context(), agentsLog, store.FactContextAssembled, c.Role.String(),
			map[string]any{"agentId": a.ID, "assembly": asm, "at": at}); err != nil {
			return err
		}
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentTrial, c.Role.String(), map[string]any{
		"agentId": a.ID, "trial": trial, "at": at, "by": c.Role.String(), "source": trial.Source,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.trial.run", map[string]any{"id": a.ID})
	var chain []string
	for _, n := range trial.Nodes {
		chain = append(chain, fmt.Sprintf("%s %dms", n.Name, n.Ms))
	}
	if len(chain) == 0 && a.Flow != nil {
		chain = a.Flow.Chain()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": a.ID, "trial": trial, "chain": chain, "source": trial.Source, "at": at,
	})
	return nil
}

// summarizeRun 把一轮执行汇成几行结论。
func summarizeRun(run *agentexec.Run) []string {
	out := make([]string, 0, 4)
	if r, ok := run.Output["risk"].(string); ok {
		out = append(out, "判定："+riskLabel(r))
	}
	if hits, ok := run.Output["hits"].([]any); ok && len(hits) > 0 {
		out = append(out, fmt.Sprintf("命中 %d 条", len(hits)))
	}
	if d, ok := run.Output["draft"].(string); ok && d != "" {
		out = append(out, "产物：话术草稿")
	}
	if esc, ok := run.Output["escalate"].(bool); ok && esc {
		out = append(out, "处置：转人工，未出自动结论")
	}
	if len(out) == 0 {
		out = append(out, "本轮无结论")
	}
	out = append(out, fmt.Sprintf("耗时 %dms", run.Ms))
	return out
}

// oneLine 把节点输出压成一行。
func oneLine(m map[string]any) string {
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	s := string(b)
	if len(s) > 160 {
		s = s[:157] + "…"
	}
	return s
}

// describeVars 把输入变量描述成一行。
func describeVars(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func riskLabel(r string) string {
	switch r {
	case "high":
		return "风险高"
	case "normal":
		return "正常"
	}
	return r
}

// agentCase 把跑过的那一条沉淀为评测用例。
//
// 用例进评测集，digest 随之变化：新用例必须先被评测过，才谈得上发布。
func (s *Server) agentCase(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTest); err != nil {
		return err
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	var in struct {
		V          string                   `json:"v"`
		In         string                   `json:"in"`
		Out        string                   `json:"out"`
		Exp        string                   `json:"exp"`
		Why        string                   `json:"why"`
		Ms         string                   `json:"ms"`
		Tok        string                   `json:"tok"`
		Score      float64                  `json:"score"`
		OK         *bool                    `json:"ok"`
		Assemblies []domain.ContextAssembly `json:"assemblies"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	v := strings.TrimSpace(in.V)
	if v == "" {
		v = "试跑沉淀 " + time.Now().Format("01-02 15:04")
	}
	cse := domain.EvalCase{
		V: v, In: in.In, Out: in.Out, Exp: in.Exp, Why: in.Why,
		Ms: in.Ms, Tok: in.Tok, Score: in.Score, OK: true,
		Assemblies: in.Assemblies,
	}
	if in.OK != nil {
		cse.OK = *in.OK
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentCase, c.Role.String(), map[string]any{
		"agentId": a.ID, "case": cse,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.case.added", map[string]any{"id": a.ID})
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "case": cse, "total": len(a.Cases) + 1})
	return nil
}

// kbDispatch 把知识库问题单派给库的 owner——不是 Agent 的 owner。
func (s *Server) kbDispatch(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBrd); err != nil {
		return err
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		return fmt.Errorf("%w：问题单序号不合法", errBadRequest)
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	if a.KB == nil || idx < 0 || idx >= len(a.KB.Issues) {
		return fmt.Errorf("%w：知识库问题单 %d 不存在", errBadRequest, idx)
	}
	if a.KB.Issues[idx].Dispatched {
		return fmt.Errorf("%w：这条问题单已经派过了", errBadRequest)
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactKBIssue, c.Role.String(), map[string]any{
		"agentId": a.ID, "idx": idx,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "kb.issue.dispatched", map[string]any{"id": a.ID, "idx": idx})
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "idx": idx})
	return nil
}

// ---------- 真人验收 ----------

// checkDecide 记录一条 AC 的判定。不通过时同时提一条 bug：
// bug 进 backlog（新事实），修完这一条重验，其它条不受影响。
func (s *Server) checkDecide(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActAcceptAT); err != nil {
		return err
	}
	var in struct {
		ST    string `json:"st"`
		Title string `json:"title"`
		Sev   string `json:"sev"`
		Steps string `json:"steps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if in.ST != domain.CheckPass && in.ST != domain.CheckFail {
		return fmt.Errorf("%w：判定只能是 pass 或 fail", errBadRequest)
	}
	if in.ST == domain.CheckFail {
		in.Title = strings.TrimSpace(in.Title)
		if in.Title == "" {
			return fmt.Errorf("%w：不提过必须写清 bug 标题", errBadRequest)
		}
		if len([]rune(in.Title)) > 120 {
			return fmt.Errorf("%w：bug 标题不超过 120 字", errBadRequest)
		}
		if in.Sev == "" {
			in.Sev = "一般"
		}
		if !domain.SevValid(in.Sev) {
			return fmt.Errorf("%w：严重级只能是 阻塞 / 严重 / 一般 / 建议", errBadRequest)
		}
	}
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	var cur *domain.Check
	for i := range v.Checks {
		if v.Checks[i].ID == c.ItemID {
			cur = &v.Checks[i]
		}
	}
	if cur == nil {
		return fmt.Errorf("%w：验收项 %s", errNotFound, c.ItemID)
	}
	now := time.Now().Format("01-02 15:04")
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactCheckDecided, c.Role.String(), map[string]any{
		"id": cur.ID, "st": in.ST, "at": now, "by": c.Role.String(),
	}); err != nil {
		return err
	}
	var bug *domain.Defect
	if in.ST == domain.CheckFail {
		d := domain.Defect{
			ID: domain.NextDefectID(v.Defects), PBC: c.PBC, AC: cur.ID,
			T: in.Title, Sev: in.Sev, Steps: in.Steps, At: now, By: c.Role.String(),
		}
		if _, err := s.Log.Append(r.Context(), c.PBC, store.FactDefect, c.Role.String(), d); err != nil {
			return err
		}
		bug = &d
		// bug 进 backlog：修复项由 X 领活，走同一条交付链路。
		// 编号在全项目范围内取，不能各单元各自从 US-01 开始——
		// 需求池是把各单元的工作项合在一起看的，编号撞了就没法指认。
		id, err := s.nextGlobalItemID(r, c.PBC)
		if err != nil {
			return err
		}
		it := store.Item{
			ID: id, Title: "修复：" + in.Title, PT: 2, St: "todo", Owner: "—", Kind: "Story",
			PBC: c.PBC, Type: "defect", Mode: "fast", Env: "test", Run: "idea",
			By: string(c.Role), At: now,
		}
		if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemCreated, c.Role.String(), it); err != nil {
			return err
		}
		s.publish(c.PBC, "defect.created", map[string]any{
			"id": d.ID, "ac": d.AC, "item": id, "by": c.Role.String(),
		})
	}
	s.publish(c.PBC, "check.decided", map[string]any{"id": cur.ID, "st": in.ST})
	writeJSON(w, http.StatusCreated, map[string]any{
		"pbc": c.PBC, "id": cur.ID, "st": in.ST, "defect": bug,
	})
	return nil
}

// nextGlobalItemID 在全项目范围内取下一个工作项编号。
//
// 需求池是各单元工作项合起来看的，编号必须全局唯一——否则「US-01」在
// 费用与质检两个单元里各有一个，谁也说不清说的是哪一条。
func (s *Server) nextGlobalItemID(r *http.Request, current string) (string, error) {
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return "", err
	}
	ids := map[string]*store.Item{}
	for _, u := range uv.Units {
		v, err := s.load(r, u.ID)
		if err != nil {
			return "", err
		}
		for id, it := range v.Items {
			ids[id] = it
		}
	}
	if v, err := s.load(r, current); err == nil {
		for id, it := range v.Items {
			ids[id] = it
		}
	}
	return nextItemID(ids)
}

// verifyChat 是验收台里的「你来当那个客户」。
//
// 回复取自 Agent 的评测用例样例，事实里标 source=sample；真正接执行器时
// 换成执行结果即可，前端只消费事实。
func (s *Server) verifyChat(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActAcceptAT); err != nil {
		return err
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" {
		return fmt.Errorf("%w：先说一句话", errBadRequest)
	}
	if len([]rune(in.Text)) > 500 {
		return fmt.Errorf("%w：一句话不超过 500 字", errBadRequest)
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	var agentID string
	for _, u := range uv.Units {
		if u.ID == c.ItemID {
			agentID = u.AgentID
		}
	}
	if agentID == "" {
		return fmt.Errorf("%w：单元 %s 没有关联 Agent，不能对话验收", errBadRequest, c.ItemID)
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[agentID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, agentID)
	}
	reply := "（按当前 AT 处理）已收到，正在核对。"
	source := "sample"
	if s.Exec != nil && a.Flow != nil {
		// 真跑一遍：把这句话当作会话原文喂进编排，回复是这一轮算出来的。
		run, err := s.Exec.Execute(r.Context(), a, map[string]any{
			"sessionText":  in.Text,
			"customerText": in.Text,
		})
		if err != nil {
			return fmt.Errorf("%w：对话验收失败：%v", errBadRequest, err)
		}
		// 试跑页可以「跑完但某几步被拒」，对话不行：客户拿到的那句话必须真的
		// 走过工具。拿一轮被拦下的执行拼回复，等于把「没读过会话」说成「读过了」。
		if blocked := run.Blocked(); len(blocked) > 0 {
			names := make([]string, 0, len(blocked))
			for _, n := range blocked {
				names = append(names, n.Name)
			}
			return fmt.Errorf("%w：编排有节点被工具白名单拒绝（%s），这一轮不能出回复", errBadRequest, strings.Join(names, "、"))
		}
		reply = replyFromRun(run)
		source = "executor"
	} else if len(a.Cases) > 0 {
		cse := a.Cases[0]
		reply = cse.Out
		if cse.Why != "" {
			reply += " 回执：" + cse.Why
		}
	}
	// 问与答一起落账：这一轮没跑通就一条都不落，账本上不会留下
	// 「客户问过、AI 答过」的假记录（拒绝时事实是零）。
	now := time.Now().Format("01-02 15:04")
	if _, err := s.Log.Append(r.Context(), c.ItemID, store.FactVerifyChat, c.Role.String(), map[string]any{
		"who": "me", "t": in.Text, "at": now, "by": c.Role.String(), "from": "customer",
	}); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), c.ItemID, store.FactVerifyChat, "x-"+agentID, map[string]any{
		"who": "ai", "t": reply, "at": now, "from": agentID, "source": source,
	}); err != nil {
		return err
	}
	s.publish(c.ItemID, "verify.chat", map[string]any{"unit": c.ItemID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"unit": c.ItemID, "agent": agentID, "reply": reply, "source": source,
	})
	return nil
}

// replyFromRun 把一轮执行的结果转成一句回复。
func replyFromRun(run *agentexec.Run) string {
	var b strings.Builder
	if r, ok := run.Output["risk"].(string); ok && r != "" {
		b.WriteString(riskLabel(r))
		if hits, ok := run.Output["hits"].([]any); ok && len(hits) > 0 {
			b.WriteString(" · 命中 " + fmt.Sprint(len(hits)) + " 条")
			if h, ok := hits[0].(map[string]any); ok {
				b.WriteString("（" + fmt.Sprint(h["rule"]) + " " + fmt.Sprint(h["why"]) + "）")
			}
		}
	} else if d, ok := run.Output["draft"].(string); ok && d != "" {
		b.WriteString(d)
		if esc, ok := run.Output["escalate"].(bool); ok && esc {
			b.WriteString("（已升级转人工）")
		}
	} else if rep, ok := run.Output["report"].(map[string]any); ok {
		b.WriteString("已生成回执")
		if r, ok := rep["risk"]; ok && r != "" {
			b.WriteString("：" + fmt.Sprint(r))
		}
	} else {
		b.WriteString("已收到，本轮无结论")
	}
	b.WriteString(fmt.Sprintf("（%dms）", run.Ms))
	return b.String()
}
