package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// v2.3 的读写在两条独立账本线上：
//
//	units —— 交付单元构成（pbc/agent/hybrid）与项目级 BRD
//	agents —— 数字员工的定义、评测、发布、灰度
//
// 这两条线不塞进每个 PBC 的日志，因为它们是项目级/全局资产；
// 但读取仍是同一个原则：Load → Project → 输出，不持有状态。

const (
	unitsLog  = "units"
	agentsLog = "agents"
)

func (s *Server) loadUnits(ctx context.Context) (*store.UnitsView, error) {
	facts, err := s.Log.Load(ctx, unitsLog)
	if err != nil {
		return nil, err
	}
	return store.ProjectUnits(facts)
}

func (s *Server) loadAgents(ctx context.Context) (*store.AgentsView, error) {
	facts, err := s.Log.Load(ctx, agentsLog)
	if err != nil {
		return nil, err
	}
	return store.ProjectAgents(facts)
}

// loadBRD 读某个项目的 BRD 版本线（v2.7 起 BRD 挂在项目上）。
func (s *Server) loadBRD(ctx context.Context, proj string) (*store.BRDView, error) {
	facts, err := s.Log.Load(ctx, unitsLog)
	if err != nil {
		return nil, err
	}
	return store.ProjectBRD(facts, proj)
}

// ---------- 视图 ----------

// UnitProgress 是侧栏用的单元摘要：构成 + HEAD + tag。
type UnitProgress struct {
	store.Unit
	Head    string        `json:"head"`
	HeadTag string        `json:"headTag,omitempty"` // HEAD 上的 tag；空 = 还没进发布编排池
	Line    string        `json:"line"`
	Tags    []string      `json:"tags"`
	Commits int           `json:"commits"`
	Deploys int           `json:"deploys"`
	Items   int           `json:"items"`
	Agent   *domain.Agent `json:"agent,omitempty"`
}

// gateSet 是 /api/state 额外给出的门禁结论：服务侧 4 闸 / Agent 侧 4 闸。
// 它由 domain 的纯函数算出，前端只展示，不重新判定。
type gateSet struct {
	PBC   []domain.Gate `json:"pbc,omitempty"`
	Agent []domain.Gate `json:"agent,omitempty"`
	All   []domain.Gate `json:"all"`
}

type stateView struct {
	*store.View
	Agent *domain.Agent `json:"agent,omitempty"`
	Gates gateSet       `json:"gates"`
	// Events 是经 pub/sub 收到的事件（收发闭环的证据）。
	Events []store.EventRecord `json:"events,omitempty"`
}

func (s *Server) stateWithAgent(ctx context.Context, v *store.View) (stateView, error) {
	sv := stateView{View: v}
	// 经 pub/sub 收到的事件：附在视图上，页面能看见「事件真的回来了」。
	sv.Events = s.eventsFor(ctx, v.PBC)
	// 单元构成以 units 日志（注册表）为准：PBC 自己的日志可能早于 v2.3，
	// 里面没有 unit.created；这时不能把 Agent 单元当成纯 PBC 来判。
	if uv, err := s.loadUnits(ctx); err != nil {
		return sv, err
	} else {
		for _, u := range uv.Units {
			if u.ID == v.PBC {
				v.Kind, v.AgentID = u.Kind, u.AgentID
				break
			}
		}
	}
	if v.AgentID == "" {
		sv.Gates.All = pbcGates(v)
		return sv, nil
	}
	av, err := s.loadAgents(ctx)
	if err != nil {
		return sv, err
	}
	a := av.Agents[v.AgentID]
	sv.Agent = a
	switch domain.ParseKind(v.Kind) {
	case domain.KindAgent:
		sv.Gates.Agent = domain.AgentGates(a)
		sv.Gates.All = sv.Gates.Agent
	case domain.KindHybrid:
		sv.Gates.PBC = pbcGates(v)
		sv.Gates.Agent = domain.AgentGates(a)
		var svc domain.SvcVerify
		if v.SvcVerify != nil {
			svc = *v.SvcVerify
		}
		// 组合单元的服务侧用验收清单替换「原型还原」，与原型 renderVerifyHybrid 一致。
		pbc3 := sv.Gates.PBC
		if len(pbc3) == 4 {
			pbc3 = pbc3[:3]
		}
		sv.Gates.All = domain.HybridGates(pbc3, svc, sv.Gates.Agent)
	default:
		sv.Gates.All = pbcGates(v)
	}
	return sv, nil
}

func pbcGates(v *store.View) []domain.Gate {
	if len(v.Commits) == 0 {
		return nil
	}
	// 视觉比对还没接：传 nil，让这条闸显示「判不了」，
	// 而不是拿默认 0 显示成「比过了、没差异」。
	return domain.CheckCommit(v.Commits[0], v.Versions, nil)
}

// state 返回完整投影：版本线、提交线、部署、批注、工作项，外加 v2.3 的
// 交付单元构成与门禁结论。
//
// 注意：这不是另一份真相——它就是 Project(facts) 的输出，和 /overview 同源。
func (s *Server) state(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	// 不带 pbc 不许回落某个默认单元（#43）：那是「拿另一个单元的完整答案冒充这个问题的答案」。
	// 前端在 S.pbcId 为空时根本不发这个请求，套件全部显式带参——去掉默认不影响谁。
	if strings.TrimSpace(r.URL.Query().Get("pbc")) == "" {
		return fmt.Errorf("%w：/api/state 需要 ?pbc=<单元 id>", errBadRequest)
	}
	// 注册表里没有的 id 是 404，和 /api/replay 一致；只有「在注册表里、日志还空」才回 200 空投影——
	// 那才是「刚建好还没事实」，和「id 写错了」必须分得开。
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	known := false
	for _, u := range uv.Units {
		if u.ID == c.PBC {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%w：单元 %s 不存在", errNotFound, c.PBC)
	}
	// 这里才是 /api/state 真正的投影出口：v3 起 v23.state 覆盖了 v2 的 state，
	// 对账必须挂在这一层，否则前端永远看不到失败（#53）。
	v, err := s.loadState(r, c.PBC)
	if err != nil {
		return err
	}
	sv, err := s.stateWithAgent(r.Context(), v)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sv)
	return nil
}

// units 是侧栏的单元列表。进度来自各自的 PBC 日志，构成来自 units 日志。
func (s *Server) units(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	out := make([]UnitProgress, 0, len(uv.Units))
	for _, u := range uv.Units {
		v, err := s.load(r, u.ID)
		if err != nil {
			return err
		}
		up := UnitProgress{Unit: u, Tags: v.Tags, Commits: len(v.Commits),
			Deploys: len(v.Deploys), Items: len(v.Items)}
		if len(v.Commits) > 0 {
			up.Head = v.Commits[0].No
			up.HeadTag = v.Commits[0].Tag
			parts := strings.Split(up.Head, ".")
			if len(parts) >= 2 {
				up.Line = parts[0] + "." + parts[1]
			}
		}
		if u.AgentID != "" {
			up.Agent = av.Agents[u.AgentID]
		}
		out = append(out, up)
	}
	writeJSON(w, http.StatusOK, map[string]any{"units": out})
	return nil
}

var unitIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)

// createUnit 建一个交付单元。构成一经确定不可改：它在账本里是三条不同的交付物。
func (s *Server) createUnit(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActPbc); err != nil {
		return err
	}
	var in struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Code    string `json:"code"`
		Kind    string `json:"kind"`
		AgentID string `json:"agentId"`
		Proj    string `json:"proj"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.ID = strings.TrimSpace(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	if !unitIDRe.MatchString(in.ID) {
		return fmt.Errorf("%w：单元 id 只能是小写字母开头的 a-z0-9-（2–24 位）", errBadRequest)
	}
	if in.Name == "" || len([]rune(in.Name)) > 40 {
		return fmt.Errorf("%w：名称必填且不超过 40 字", errBadRequest)
	}
	k := domain.ParseKind(in.Kind)
	// v3.0 起只有两种构成：pbc / agent。组合不是单元的属性——
	// 一个单元只有一个 tag，硬拆会在回滚时说不清回哪一半；
	// 「服务 + Agent 一起上」改到发布编排里勾进同一批次表达。
	if k == domain.KindHybrid {
		return fmt.Errorf("%w：不再支持 hybrid（PBC + Agent）单元：一个单元只有一个 tag，"+
			"要一起上就拆成两个单元、在发布编排里放进同一批次", errBadRequest)
	}
	if in.Kind != "" && !k.Valid() {
		return fmt.Errorf("%w：kind 只能是 pbc / agent", errBadRequest)
	}
	if k != domain.KindPBC && in.AgentID == "" {
		return fmt.Errorf("%w：agent 单元必须指定 agentId（交付的 Agent 产物）", errBadRequest)
	}
	// 交付单元挂在项目下：项目必须存在（v2.7）。
	// 老账本还没有 projects 日志：此时默认项目视为存在，升级不打断。
	in.Proj = strings.TrimSpace(in.Proj)
	if in.Proj == "" {
		in.Proj = store.DefaultProject
	}
	ok, err := s.projectExists(r.Context(), in.Proj)
	if err != nil {
		return err
	}
	if !ok {
		pv, err := s.loadProjects(r.Context())
		if err != nil {
			return err
		}
		if !(pv.Seq == 0 && in.Proj == store.DefaultProject) {
			return fmt.Errorf("%w：项目 %s 不存在", errBadRequest, in.Proj)
		}
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	for _, u := range uv.Units {
		if u.ID == in.ID {
			return fmt.Errorf("%w：单元 %s 已存在", errBadRequest, in.ID)
		}
	}
	if k != domain.KindPBC {
		av, err := s.loadAgents(r.Context())
		if err != nil {
			return err
		}
		if av.Agents[in.AgentID] == nil {
			return fmt.Errorf("%w：Agent %s 不存在", errBadRequest, in.AgentID)
		}
	}
	code := strings.TrimSpace(in.Code)
	if code == "" {
		code = "PBC-" + strings.ToUpper(in.ID)
	}
	if _, err := s.Log.Append(r.Context(), unitsLog, store.FactUnitCreated, c.Role.String(), map[string]any{
		"id": in.ID, "name": in.Name, "code": code, "kind": k.String(), "agentId": in.AgentID, "proj": in.Proj,
	}); err != nil {
		return err
	}
	// 单元自己的日志也要有 unit.created：/api/state 的 kind / name / agentId 由此而来。
	// 同一条写入同时落两处（注册表 + 单元日志），两者都是投影的输出，不会分叉。
	if _, err := s.Log.Append(r.Context(), in.ID, store.FactUnitCreated, c.Role.String(), map[string]any{
		"name": in.Name, "kind": k.String(), "agentId": in.AgentID,
	}); err != nil {
		return err
	}
	s.publish(in.ID, "unit.created", map[string]any{"id": in.ID})
	writeJSON(w, http.StatusCreated, map[string]any{"id": in.ID, "kind": k.String(), "proj": in.Proj})
	return nil
}

// ---------- BRD ----------

// brdProj 取请求里的项目号：?proj=xxx；不传就是种子项目。
func brdProj(r *http.Request) string {
	p := strings.TrimSpace(r.URL.Query().Get("proj"))
	if p == "" {
		return store.DefaultProject
	}
	return p
}

func (s *Server) brd(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	v, err := s.loadBRD(r.Context(), brdProj(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

// saveBRD 每次保存都落一个草稿版本，和制品版本线同一规则。
func (s *Server) saveBRD(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBrd); err != nil {
		return err
	}
	var in struct {
		Text string `json:"text"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return fmt.Errorf("%w：BRD 不能为空", errBadRequest)
	}
	if len([]rune(in.Text)) > 200000 {
		return fmt.Errorf("%w：BRD 超过 20 万字", errBadRequest)
	}
	cur, err := s.loadBRD(r.Context(), brdProj(r))
	if err != nil {
		return err
	}
	ver := nextBRDVersion(cur)
	// 上一版草稿要被新草稿取代；ProjectBRD 会在重放时处理 superseded。
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "保存 BRD 草稿"
	}
	if _, err := s.Log.Append(r.Context(), unitsLog, store.FactBRDSaved, c.Role.String(), map[string]any{
		"proj": brdProj(r),
		"v":    ver, "at": time.Now().Format("01-02 15:04"), "by": c.Role.String(),
		"st": "draft", "note": note, "text": in.Text,
	}); err != nil {
		return err
	}
	s.publish("units", "brd.saved", map[string]any{"v": ver, "proj": brdProj(r)})
	writeJSON(w, http.StatusCreated, map[string]any{"v": ver, "proj": brdProj(r)})
	return nil
}

func nextBRDVersion(cur *store.BRDView) string {
	if len(cur.Vers) == 0 {
		// 第一版是 0.1，不是 1.1：新项目刚开始写，还没到「能对外」的版本。
		return "v0.1"
	}
	// 0.x 阶段就在 0.x 上递增（0.1 → 0.2）：把 0.1 强行提到 1.2 会假装
	// 这份 BRD 已经「到 1 了」，评审时会误读成熟度。
	major, minor := -1, -1
	for _, v := range cur.Vers {
		var a, b int
		if _, err := fmt.Sscanf(v.V, "v%d.%d", &a, &b); err == nil {
			if a > major || (a == major && b > minor) {
				major, minor = a, b
			}
		}
	}
	if major < 0 {
		return "v0.1"
	}
	if major == 0 {
		return fmt.Sprintf("v0.%d", minor+1)
	}
	return fmt.Sprintf("v1.%d", minor+1)
}

// ---------- Agent ----------

type agentView struct {
	*domain.Agent
	Gates []domain.Gate `json:"gates"`
	Units []string      `json:"units,omitempty"`
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	refs := map[string][]string{}
	for _, u := range uv.Units {
		if u.AgentID != "" {
			refs[u.AgentID] = append(refs[u.AgentID], u.ID)
		}
	}
	out := make([]agentView, 0, len(av.Order))
	for _, id := range av.Order {
		a := av.Agents[id]
		if a == nil {
			continue
		}
		out = append(out, agentView{Agent: a, Gates: domain.AgentGates(a), Units: refs[id]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
	return nil
}

func (s *Server) agent(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	var refs []string
	for _, u := range uv.Units {
		if u.AgentID == a.ID {
			refs = append(refs, u.ID)
		}
	}
	writeJSON(w, http.StatusOK, agentView{Agent: a, Gates: domain.AgentGates(a), Units: refs})
	return nil
}

// agentEval 接收评测执行器的一次运行结果。
// 注意：BFF 自己没有评测执行器——没有结果就明确拒绝，不编一个通过率出来。
func (s *Server) agentEval(w http.ResponseWriter, r *http.Request, c reqCtx) error {
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
	a.EnsureDigest()
	// 评测必须跑在当前构建产物上：编排改了没构建，先构建再评测。
	if a.BuiltDigest == "" || a.BuiltDigest != a.Digest {
		return fmt.Errorf("%w：%s 有未构建改动，先「构建」再评测", errBadRequest, a.ID)
	}
	var in struct {
		Pass  int               `json:"pass"`
		Total int               `json:"total"`
		Ver   string            `json:"ver"`
		Cost  string            `json:"cost"`
		P50   string            `json:"p50"`
		P95   string            `json:"p95"`
		Tok   string            `json:"tok"`
		Cases []domain.EvalCase `json:"cases"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		return fmt.Errorf("%w：缺少评测结果（本服务不代跑评测）：%v", errBadRequest, err)
	}
	if in.Total <= 0 || in.Pass < 0 || in.Pass > in.Total {
		return fmt.Errorf("%w：评测结果必须给出 total>0 且 0<=pass<=total（本服务不代跑评测）", errBadRequest)
	}
	ver := in.Ver
	if ver == "" {
		ver = a.Ver
	}
	run := domain.EvalRun{
		ID: fmt.Sprintf("exp-%04d", time.Now().Unix()%10000), At: time.Now().Format("01-02 15:04"),
		Ver: ver, Pass: in.Pass, Total: in.Total, Cost: in.Cost, P50: in.P50, P95: in.P95, Tok: in.Tok,
		Digest: a.BuiltDigest,
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentEvalRun, c.Role.String(), map[string]any{
		"agentId": a.ID, "run": run,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.eval.run", map[string]any{"id": a.ID, "pass": in.Pass, "total": in.Total})
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "run": run})
	return nil
}

// agentBaseline 把某次运行设为对比基线。纯账本操作，不需要外部执行器。
func (s *Server) agentBaseline(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTest); err != nil {
		return err
	}
	var in struct {
		RunID string `json:"runId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	found := false
	for _, run := range a.Runs {
		if run.ID == in.RunID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w：运行 %s 不存在", errBadRequest, in.RunID)
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentBaseline, c.Role.String(), map[string]any{
		"agentId": a.ID, "runId": in.RunID,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.baseline", map[string]any{"id": a.ID, "runId": in.RunID})
	writeJSON(w, http.StatusOK, map[string]any{"id": a.ID, "runId": in.RunID})
	return nil
}

// agentPublish 注册新版本：四闸不过就不许发布，和 PBC 打 tag 同一件事。
func (s *Server) agentPublish(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTag); err != nil {
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
	gs := domain.AgentGates(a)
	if !domain.GatesPass(gs) {
		var bad []string
		for _, g := range gs {
			if !g.OK {
				bad = append(bad, g.Label+"（"+g.Detail+"）")
			}
		}
		return fmt.Errorf("%w：Agent 四闸未过：%s", errBadRequest, strings.Join(bad, "、"))
	}
	a.EnsureDigest()
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentPublish, c.Role.String(), map[string]any{
		"agentId": a.ID, "ver": a.Ver, "digest": a.Digest, "tag": "v" + a.Ver,
		"at": time.Now().Format("01-02 15:04"),
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.published", map[string]any{"id": a.ID, "ver": a.Ver})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": a.ID, "ver": a.Ver, "digest": a.Digest})
	return nil
}

// agentRollout 灰度放量：只许 10 → 50 → 100，不能跳档。
func (s *Server) agentRollout(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActDeploy); err != nil {
		return err
	}
	var in struct {
		Pct int `json:"pct"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	av, err := s.loadAgents(r.Context())
	if err != nil {
		return err
	}
	a := av.Agents[c.ItemID]
	if a == nil {
		return fmt.Errorf("%w：Agent %s", errNotFound, c.ItemID)
	}
	if !a.Published {
		return fmt.Errorf("%w：%s 尚未发布，不能放量", errBadRequest, a.ID)
	}
	if in.Pct != 10 && in.Pct != 50 && in.Pct != 100 {
		return fmt.Errorf("%w：放量档位只能是 10 / 50 / 100", errBadRequest)
	}
	if in.Pct <= a.Rollout {
		return fmt.Errorf("%w：当前已是 %d%%，不能回退到 %d%%（回退请用「回滚」）", errBadRequest, a.Rollout, in.Pct)
	}
	if a.Rollout == 10 && in.Pct == 100 {
		return fmt.Errorf("%w：不能跳档，先放到 50%%", errBadRequest)
	}
	if a.Rollout == 0 && in.Pct != 10 {
		return fmt.Errorf("%w：首发只能从 10%% 开始", errBadRequest)
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentRollout, c.Role.String(), map[string]any{
		"agentId": a.ID, "pct": in.Pct,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.rollout", map[string]any{"id": a.ID, "pct": in.Pct})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": a.ID, "pct": in.Pct})
	return nil
}

// agentRollback 回滚到上一版：流量归零，版本指针回到上一个已发布版本。
func (s *Server) agentRollback(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActDeploy); err != nil {
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
	if !a.Published {
		return fmt.Errorf("%w：%s 尚未发布，没有可回滚的版本", errBadRequest, a.ID)
	}
	to := ""
	for _, r := range a.Runs {
		if r.Ver != a.Ver {
			to = r.Ver
			break
		}
	}
	if _, err := s.Log.Append(r.Context(), agentsLog, store.FactAgentRollback, c.Role.String(), map[string]any{
		"agentId": a.ID, "to": to,
	}); err != nil {
		return err
	}
	s.publish(agentsLog, "agent.rollback", map[string]any{"id": a.ID, "to": to})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": a.ID, "to": to})
	return nil
}

// ---------- 服务验收（组合单元） ----------

// svcVerify 接收服务侧验收结果。与 eval 一样：BFF 不代跑测试，
// 结果由测试执行器提交；没有结果就不伪造「全过」。
func (s *Server) svcVerify(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTest); err != nil {
		return err
	}
	var in domain.SvcVerify
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if in.Total <= 0 || in.Pass < 0 || in.Pass > in.Total {
		return fmt.Errorf("%w：验收结果必须给出 total>0 且 0<=pass<=total", errBadRequest)
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	found := false
	for _, u := range uv.Units {
		if u.ID == c.ItemID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w：%s", errNotFound, c.ItemID)
	}
	if _, err := s.Log.Append(r.Context(), c.ItemID, store.FactSvcVerified, c.Role.String(), in); err != nil {
		return err
	}
	s.publish(c.ItemID, "svc.verified", map[string]any{"pass": in.Pass, "total": in.Total})
	writeJSON(w, http.StatusCreated, in)
	return nil
}

// facts 是右栏「运行时」的数据源：真实的事实流，不是模拟日志。
// 只给 seq/at/type/actor，不带 payload——运行时面板要的是「谁在什么时候做了什么」。
// facts 列出某本账的事实。?limit=N 只回最近 N 条：
// 页面只要一条事实流给人看，不需要每轮 refresh 都把整本账（几百条、还在长）从 Dapr 走一遍——
// 那会占住 refresh，切角色后权限位要等它跑完才更新（#46）。
// 不带 limit 仍然回全量（验收脚本核对账本用）。
func (s *Server) facts(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	facts, err := s.Log.Load(r.Context(), c.PBC)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(facts))
	for _, f := range facts {
		out = append(out, map[string]any{"seq": f.Seq, "at": f.At, "type": f.Type, "actor": f.Actor})
	}
	// 新的在前：和运行时日志的阅读顺序一致。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"pbc": c.PBC, "facts": out})
	return nil
}

// ---------- 制品草稿 ----------

// 制品草稿：文档（prd/proto/ad/at/backlog）存 md，代码存文件树。
// 保存即新版本（source=手动编辑），进工作区；提交由 workflow 决定，
// 所以这里只落到 draft 态，绝不直接改 HEAD。
var artKinds = map[domain.Artifact]bool{
	domain.PRD: true, domain.Proto: true, domain.AD: true,
	domain.AT: true, domain.Backlog: true, domain.Code: true,
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	// 编辑制品是「产品确认」动作：PA 有 acceptProto/pbc，BA 有 brd/acceptAT。
	// 统一到 ActPbc（PA 专属）会挡住 BA 写 PRD，所以按制品类型判。
	var in struct {
		Artifact string            `json:"artifact"`
		Text     string            `json:"text"`
		Files    map[string]string `json:"files"`
		Note     string            `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	t := domain.Artifact(in.Artifact)
	if !artKinds[t] {
		return fmt.Errorf("%w：未知制品 %q", errBadRequest, in.Artifact)
	}
	// 编辑权按制品类型落到真正负责它的角色：
	//   prd / at → BA（brd / acceptAT）
	//   proto / ad → PA（acceptProto / pbc）
	//   backlog → BA（backlog）
	//   code → DA（build）——代码是交付物，不是产品文档
	if err := Require(c.Role, actFor(t)); err != nil {
		return err
	}
	if t == domain.Code {
		if len(in.Files) == 0 {
			return fmt.Errorf("%w：代码草稿必须给出 files", errBadRequest)
		}
	} else if strings.TrimSpace(in.Text) == "" {
		return fmt.Errorf("%w：内容为空，不能保存", errBadRequest)
	}

	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	latest := func(x domain.Artifact) string { return domain.Latest(v.Versions, x) }
	cur := latest(t)
	nv, err := domain.NextVersion(cur)
	if err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	// 同制品已有草稿时，新草稿取代它（superseded 由投影处理）。
	ver := domain.Version{
		T: t, V: nv, At: time.Now().Format("01-02 15:04"), By: workerFor(t),
		Source: "手动编辑", DF: domain.DeriveRefs(t, latest), St: domain.StDraft,
		Note: strings.TrimSpace(in.Note),
	}
	if ver.Note == "" {
		ver.Note = "人工编辑"
	}
	if t == domain.Code {
		ver.Files = in.Files
	} else {
		ver.MD = in.Text
	}
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactArtifactDrafted, c.Role.String(), ver); err != nil {
		return err
	}
	s.publish(c.PBC, "artifact.drafted", map[string]any{"artifact": string(t), "version": nv})
	writeJSON(w, http.StatusCreated, map[string]any{"artifact": t, "version": nv})
	return nil
}

// actFor 是制品编辑权对应的动作名。前端只做展示，真正的判定在这里。
func actFor(t domain.Artifact) Act {
	switch t {
	case domain.PRD:
		return ActBrd
	case domain.AT, domain.Backlog:
		return ActAcceptAT
	case domain.Code:
		return ActBuild
	default: // proto / ad
		return ActAcceptProto
	}
}

// workerFor 是制品 → 负责的 X worker，与原型 ART_WORKER 一致。
func workerFor(t domain.Artifact) string {
	switch t {
	case domain.PRD, domain.Backlog:
		return "x-ba"
	case domain.Proto:
		return "x-ui"
	case domain.AD, domain.Code:
		return "x-fe"
	case domain.AT:
		return "x-qa"
	}
	return "x-fe"
}

// ---------- 批注 ----------

func (s *Server) addAnnotation(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActAcceptProto); err != nil {
		return err
	}
	var in store.Anno
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return fmt.Errorf("%w：批注内容不能为空", errBadRequest)
	}
	in.By, in.At = c.Role.String(), time.Now().Format("01-02 15:04")
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactAnnotationAdded, c.Role.String(), in); err != nil {
		return err
	}
	s.publish(c.PBC, "annotation.added", nil)
	writeJSON(w, http.StatusCreated, in)
	return nil
}

// ---------- 收件箱 / 通知 ----------

// inbox 是工作台首页的数据源：跨所有交付单元的工作项。
func (s *Server) inbox(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	type inboxItem struct {
		Task
		Unit string `json:"unit"`
		Kind string `json:"unitKind"`
	}
	out := make([]inboxItem, 0, 16)
	for _, u := range uv.Units {
		v, err := s.load(r, u.ID)
		if err != nil {
			return err
		}
		for _, it := range v.Items {
			t := toTask(it)
			t.PBC = u.ID
			out = append(out, inboxItem{Task: t, Unit: u.Name, Kind: u.Kind})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{
		"role": c.Role, "name": roleNames[c.Role], "acts": roleActs[c.Role], "items": out,
	})
	return nil
}

// notifications 从账本事实里挑出「人需要知道的变化」，不另存一份通知表。
func (s *Server) notifications(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	type note struct {
		At   string `json:"at"`
		Type string `json:"type"`
		Text string `json:"text"`
		Unit string `json:"unit"`
	}
	var out []note
	for _, u := range uv.Units {
		facts, err := s.Log.Load(r.Context(), u.ID)
		if err != nil {
			return err
		}
		for _, f := range facts {
			switch f.Type {
			case store.FactItemPublished, store.FactItemBlocked, store.FactTagCreated,
				store.FactDeploySucceeded, store.FactSvcVerified:
				out = append(out, note{At: f.At, Type: f.Type, Text: u.Name + " · " + factSummary(f), Unit: u.ID})
			}
		}
	}
	af, err := s.Log.Load(r.Context(), agentsLog)
	if err != nil {
		return err
	}
	for _, f := range af {
		switch f.Type {
		case store.FactAgentPublish, store.FactAgentRollout, store.FactAgentRollback:
			out = append(out, note{At: f.At, Type: f.Type, Text: factSummary(f), Unit: agentsLog})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > 60 {
		out = out[:60]
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
	return nil
}

func factSummary(f store.Fact) string {
	var p map[string]any
	_ = json.Unmarshal(f.Payload, &p)
	get := func(k string) string {
		if v, ok := p[k]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	switch f.Type {
	case store.FactItemPublished:
		return fmt.Sprintf("%s 发布 %s（%s）", get("id"), get("tag"), get("version"))
	case store.FactItemBlocked:
		return fmt.Sprintf("%s 受阻：%s", get("id"), get("reason"))
	case store.FactTagCreated:
		return fmt.Sprintf("打了 tag %s", get("tag"))
	case store.FactDeploySucceeded:
		return fmt.Sprintf("部署 %s 成功", get("tag"))
	case store.FactSvcVerified:
		return fmt.Sprintf("服务验收 %s/%s", get("pass"), get("total"))
	case store.FactAgentPublish:
		return fmt.Sprintf("Agent %s 发布 v%s", get("agentId"), get("ver"))
	case store.FactAgentRollout:
		return fmt.Sprintf("Agent %s 放量到 %s%%", get("agentId"), get("pct"))
	case store.FactAgentRollback:
		return fmt.Sprintf("Agent %s 回滚到 v%s", get("agentId"), get("to"))
	}
	return f.Type
}
