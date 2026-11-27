package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 本文件是 v3.2 的「打 tag + 项目级发布编排」。
//
// 单元只做到「可以交付」：机器跑完五步，停在第 6 步（打 tag）。
// 打 tag 是人在验证页按下的动作——这里重新校验闸门，过了才落 tag.created。
// 上哪个环境、和谁一起上，由项目级批次决定：批次锁住 tag 与 digest，
// 只能整批回滚，没打 tag 的单元进不来。

const releasesLog = "releases"

// ReleaseEnvs 是发布批次可选的环境。四个环境不是四套系统，
// 是同一份编排的四个规格档；生产只允许通过批次变更。
var ReleaseEnvs = []string{"SIT", "UAT", "预发", "生产"}

func (s *Server) loadReleases(ctx context.Context) (*store.ReleasesView, error) {
	facts, err := s.Log.Load(ctx, releasesLog)
	if err != nil {
		return nil, err
	}
	return store.ProjectReleases(facts)
}

// unitGates 按单元构成给出闸门。PBC 看四闸（含「判不了」的原型还原），
// Agent 看四闸；hybrid 只作为历史数据保留，新数据不再产生。
func (s *Server) unitGates(ctx context.Context, v *store.View, u store.Unit) []domain.Gate {
	switch domain.ParseKind(u.Kind) {
	case domain.KindAgent:
		av, err := s.loadAgents(ctx)
		if err != nil {
			return []domain.Gate{{Key: "agent", Label: "Agent 闸", OK: false, Detail: err.Error()}}
		}
		return domain.AgentGates(av.Agents[u.AgentID])
	case domain.KindHybrid:
		pbc := pbcGates(v)
		sv := domain.SvcVerify{}
		if v.SvcVerify != nil {
			sv = *v.SvcVerify
		}
		av, _ := s.loadAgents(ctx)
		var a *domain.Agent
		if av != nil {
			a = av.Agents[u.AgentID]
		}
		return domain.HybridGates(pbc, sv, domain.AgentGates(a))
	default:
		return pbcGates(v)
	}
}

// checkSummary 统计验收清单在打 tag 那一刻的分布。
// 只存计数：提交事实（或历史 check.defined）记录清单，check.decided 记录判定。
func checkSummary(cs []domain.Check) store.CheckSummary {
	s := store.CheckSummary{Total: len(cs)}
	for _, c := range cs {
		switch c.ST {
		case domain.CheckPass:
			s.Pass++
		case domain.CheckFail:
			s.Fail++
		default:
			s.Todo++
		}
	}
	return s
}

// ---------- 打 tag ----------

// tagUnit 是人在验证页按下的「打 tag 固化交付物」。
//
// 打 tag 前核对构建、测试、人工验收与交付闸门：
//  1. HEAD 有真构建的 digest；
//  2. 最近一次测试针对 HEAD 且全过；
//  3. PBC 有当前提交的人工验收清单且全过；
//  4. 闸门全过（判不了的不挡，但也不当成通过）。
func (s *Server) tagUnit(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTag); err != nil {
		return err
	}
	id := r.PathValue("id")
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	var u store.Unit
	found := false
	for _, x := range uv.Units {
		if x.ID == id {
			u, found = x, true
		}
	}
	if !found {
		return fmt.Errorf("%w：单元 %s 不存在", errNotFound, id)
	}
	v, err := s.load(r, id)
	if err != nil {
		return err
	}
	if len(v.Commits) == 0 {
		return fmt.Errorf("%w：还没有提交，无法打 tag", errBadRequest)
	}
	head := v.Commits[0]
	if head.Tag != "" {
		return fmt.Errorf("%w：HEAD %s 已经打过 tag %s", errBadRequest, head.No, head.Tag)
	}
	if head.Digest == "" {
		return fmt.Errorf("%w：HEAD %s 没有构建 digest，先构建", errBadRequest, head.No)
	}
	if v.Test == nil || v.Test.Commit != head.No {
		return fmt.Errorf("%w：HEAD %s 没有对应的测试结果", errBadRequest, head.No)
	}
	if v.Test.Pass != v.Test.Total {
		return fmt.Errorf("%w：测试未全过（%d/%d），不能打 tag", errBadRequest, v.Test.Pass, v.Test.Total)
	}
	if domain.ParseKind(u.Kind) != domain.KindAgent && (len(v.Checks) == 0 || v.CheckCommit != head.No) {
		return fmt.Errorf("%w：HEAD %s 没有对应的人工验收清单，须提交带验收项的 AT 并由 BA 判定", errBadRequest, head.No)
	}
	// Agent 仍按既有口径检查已有清单和评测闸。
	if len(v.Checks) > 0 {
		left, pass := 0, 0
		for _, c := range v.Checks {
			switch c.ST {
			case domain.CheckPass:
				pass++
			default:
				left++
			}
		}
		if left > 0 {
			return fmt.Errorf("%w：还差 %d 条验收未通过（%d/%d），不能打 tag", errBadRequest, left, pass, len(v.Checks))
		}
	}
	gates := s.unitGates(r.Context(), v, u)
	if !domain.GatesPass(gates) {
		bad := ""
		for _, g := range gates {
			if !g.OK {
				bad = g.Label + "：" + g.Detail
				break
			}
		}
		return fmt.Errorf("%w：闸门未过：%s", errBadRequest, bad)
	}
	tag := domain.NextTag(v.Tags)
	// 先把「凭什么能打这个 tag」定格成事实，再落 tag 本身。
	//
	// 顺序有讲究：gate.evaluated 的 seq 恰好紧挨着 tag.created。
	// 回放时以 tag.created 的 seq 为界，切出来的窗口里必然含这组闸；
	// 反过来先落 tag 再补证据，中间崩一次就会出现「有 tag、没依据」的账。
	ev := store.GateEvaluation{Tag: tag, Commit: head.No, Gates: gates,
		Checks: checkSummary(v.Checks), By: string(c.Role)}
	if v.Test != nil {
		t := *v.Test
		ev.Test = &t
	}
	if _, err := s.Log.Append(r.Context(), id, store.FactGateEvaluated, string(c.Role), ev); err != nil {
		return err
	}
	p := map[string]any{
		"tag": tag, "commit": head.No, "treeSha": head.TreeSHA, "digest": head.Digest, "by": string(c.Role),
	}
	if _, err := s.Log.Append(r.Context(), id, store.FactTagCreated, string(c.Role), p); err != nil {
		return err
	}
	s.publish(id, "tag.created", map[string]any{"pbc": id, "tag": tag, "commit": head.No})
	writeJSON(w, http.StatusCreated, map[string]any{
		"unit": id, "tag": tag, "commit": head.No, "digest": head.Digest, "pooled": true,
	})
	return nil
}

// ---------- 发布编排 ----------

type orchUnit struct {
	store.Unit
	Head   string        `json:"head"`
	Tag    string        `json:"tag"` // HEAD 上的 tag；空 = 未进池
	Digest string        `json:"digest"`
	Binary bool          `json:"binary"`
	Ready  bool          `json:"ready"`
	Reason string        `json:"reason,omitempty"`
	Gates  []domain.Gate `json:"gates"`
}

type orchView struct {
	Proj    string             `json:"proj"`
	Units   []orchUnit         `json:"units"`
	Batches []releaseBatchView `json:"batches"`
	Envs    []string           `json:"envs"`
}

// orch 是项目级发布编排的读接口：编排链上的单元 + 就绪判断 + 历史批次。
func (s *Server) orch(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	proj := r.PathValue("id")
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	exists := false
	for _, p := range pv.Projects {
		if p.ID == proj {
			exists = true
		}
	}
	if !exists {
		return fmt.Errorf("%w：项目 %s 不存在", errNotFound, proj)
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	out := orchView{Proj: proj, Envs: ReleaseEnvs}
	for _, u := range uv.Units {
		if u.Proj != proj {
			continue
		}
		v, err := s.load(r, u.ID)
		if err != nil {
			return err
		}
		ou := orchUnit{Unit: u}
		if len(v.Commits) > 0 {
			h := v.Commits[0]
			ou.Head, ou.Tag, ou.Digest = h.No, h.Tag, h.Digest
			ou.Binary = v.Binaries[h.No]
		}
		ou.Gates = s.unitGates(r.Context(), v, u)
		// 就绪 = HEAD 上已经打了 tag。tag 本身就是「当时闸门与验收都过了」的凭据，
		// 不要求现在再跑一遍测试——否则一个已交付的单元会因为别的流程改过账本而「不就绪」。
		switch {
		case ou.Head == "":
			ou.Reason = "还没有提交"
		case ou.Digest == "":
			ou.Reason = "未构建"
		case ou.Tag == "":
			ou.Reason = "还没打 tag"
		default:
			ou.Ready = true
		}
		out.Units = append(out.Units, ou)
	}
	rv, err := s.loadReleases(r.Context())
	if err != nil {
		return err
	}
	for _, b := range rv.Batches {
		if b.Proj == proj {
			out.Batches = append(out.Batches, s.releaseBatchView(b))
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// release 发一个批次。
//
// 这不是「把单元发出去」，而是「锁住这一批 tag 与 digest，按顺序上环境」：
// 生成后不可改；失败就整批不落地（已起的进程全部收回）。
func (s *Server) release(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTag); err != nil {
		return err
	}
	proj := r.PathValue("id")
	var in struct {
		Env   string   `json:"env"`
		Mode  string   `json:"mode"`
		Units []string `json:"units"`
		Note  string   `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	envOK := false
	for _, e := range ReleaseEnvs {
		if e == in.Env {
			envOK = true
		}
	}
	if !envOK {
		return fmt.Errorf("%w：环境只能是 SIT / UAT / 预发 / 生产", errBadRequest)
	}
	if in.Mode == "" {
		in.Mode = "full"
	}
	if in.Mode != "full" && in.Mode != "gray" {
		return fmt.Errorf("%w：方式只能是 full（全量）或 gray（灰度）", errBadRequest)
	}
	if len(in.Units) == 0 {
		return fmt.Errorf("%w：至少勾选一个单元", errBadRequest)
	}

	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	byID := map[string]store.Unit{}
	for _, u := range uv.Units {
		if u.Proj == proj {
			byID[u.ID] = u
		}
	}

	// 先把整批锁定清单算出来：任何一个没就绪就整批不发，不落地半批。
	var locked []store.ReleaseUnit
	var views = map[string]*store.View{}
	builtBy := map[string]string{}
	for _, id := range in.Units {
		u, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w：单元 %s 不属于项目 %s", errBadRequest, id, proj)
		}
		v, err := s.load(r, id)
		if err != nil {
			return err
		}
		if len(v.Commits) == 0 {
			return fmt.Errorf("%w：%s 还没有提交", errBadRequest, id)
		}
		h := v.Commits[0]
		if h.Tag == "" {
			return fmt.Errorf("%w：%s 还没打 tag，不能进批次", errBadRequest, u.Name)
		}
		if h.Digest == "" {
			return fmt.Errorf("%w：%s 的 HEAD 没有 digest", errBadRequest, u.Name)
		}
		locked = append(locked, store.ReleaseUnit{
			ID: u.ID, Name: u.Name, Kind: u.Kind, AgentID: u.AgentID,
			Tag: h.Tag, Commit: h.No, Digest: h.Digest, Binary: v.Binaries[h.No],
		})
		views[u.ID] = v
		// 记下 digest 是哪次构建落账的：产物找不到时要能回答「谁算出这个 digest」，
		// 否则「不在仓库里」只是一句把排查推给人的话（#52）。
		{
			facts, err := s.Log.Load(r.Context(), id)
			if err != nil {
				return err
			}
			// 取最后一条产出同一 digest 的构建：账本上多次构建同一个字节是正常的。
			for _, f := range facts {
				if f.Type != store.FactBuildSucceeded {
					continue
				}
				var p struct {
					Commit string `json:"commit"`
					Digest string `json:"digest"`
				}
				if json.Unmarshal(f.Payload, &p) == nil && p.Digest == h.Digest {
					builtBy[id] = fmt.Sprintf("build.succeeded seq=%d · actor=%s · %s", f.Seq, f.Actor, f.At)
				}
			}
		}
	}

	if s.Pipe == nil {
		return fmt.Errorf("执行侧未接入，无法发布")
	}
	// 真部署：PBC 产物起进程并等 /healthz；Agent 产物没有进程，如实标 activated。
	var done []store.ReleaseUnit
	fail := func(reason string) error {
		for _, unit := range done {
			s.Pipe.Dep.StopRuntime(unit.ID, in.Env, unit.RuntimeID)
		}
		return fmt.Errorf("%w：%s", errBadRequest, reason)
	}
	for i := range locked {
		l := &locked[i]
		if !l.Binary {
			l.Status = "activated"
			continue
		}
		out, err := s.Pipe.Deploy(r.Context(), l.ID, in.Env, l.Commit, l.Digest, true)
		if err != nil {
			// 产物找不到时把「谁算出这个 digest、仓库在哪」一起报出来：
			// 换 ARTIFACT_DIR 或清掉目录后重启，账本还在、字节没了——
			// 这句话要能让人一眼看出是哪一头没对上（#52）。
			where := fmt.Sprintf("仓库根 %s", s.Pipe.Root())
			if src := builtBy[l.ID]; src != "" {
				where += " · digest 出自 " + src
			} else {
				where += " · 账本里没有产出该 digest 的 build.succeeded"
			}
			return fail("部署 " + l.Name + "（" + l.Commit + "）失败：" + err.Error() + "；" + where)
		}
		l.URL, l.RuntimeID, l.Status = out.URL, out.RuntimeID, "deployed"
		if in.Env == "SIT" && byID[l.ID].Profile == domain.ProfileMaterialInbound {
			l.BrowserURL = materialURL(l.ID, in.Env) + "?commit=" + l.Commit
		}
		done = append(done, *l)
		// 部署事实落在单元自己的账本里：这条线的「谁在哪个环境、部署了哪个字节」可查。
		d := store.Deploy{ID: "release", Tag: l.Tag, Commit: l.Commit, URL: out.URL,
			At: stampNow(), By: string(c.Role), Status: "success"}
		if _, err := s.Log.Append(r.Context(), l.ID, store.FactDeploySucceeded, string(c.Role), d); err != nil {
			return err
		}
	}

	b := store.ReleaseBatch{
		Proj: proj, Env: in.Env, Mode: in.Mode, Units: locked,
		By: string(c.Role), At: stampNow(), Status: "active",
	}
	if err := s.recordReleaseBatch(r.Context(), &b); err != nil {
		for _, unit := range done {
			s.Pipe.Dep.StopRuntime(unit.ID, in.Env, unit.RuntimeID)
		}
		return err
	}
	s.publish("releases", "release.batch.created", map[string]any{"id": b.ID, "proj": proj})
	writeJSON(w, http.StatusCreated, s.releaseBatchView(b))
	return nil
}

// rollback 整批回滚。批次是发布单位：只回一半会留下说不清的组合。
func (s *Server) rollback(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTag); err != nil {
		return err
	}
	proj := r.PathValue("id")
	var in struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	rv, err := s.loadReleases(r.Context())
	if err != nil {
		return err
	}
	var b *store.ReleaseBatch
	for i := range rv.Batches {
		if rv.Batches[i].ID == in.ID && rv.Batches[i].Proj == proj {
			b = &rv.Batches[i]
		}
	}
	if b == nil {
		return fmt.Errorf("%w：批次 %s 不存在", errNotFound, in.ID)
	}
	if b.Status == "rolledback" {
		return fmt.Errorf("%w：批次 %s 已经回滚过", errBadRequest, in.ID)
	}
	for _, unit := range b.Units {
		if instance, ok := s.releaseUnitInstance(*b, unit); ok {
			s.Pipe.Dep.StopRuntime(unit.ID, b.Env, instance.RuntimeID)
		}
	}
	if _, err := s.Log.Append(r.Context(), releasesLog, store.FactReleaseRollback, string(c.Role),
		map[string]any{"id": b.ID, "proj": b.Proj, "by": string(c.Role), "at": stampNow(), "note": in.Note}); err != nil {
		return err
	}
	s.publish("releases", "release.batch.rolledback", map[string]any{"id": b.ID})
	writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "status": "rolledback"})
	return nil
}

func (s *Server) recordReleaseBatch(ctx context.Context, batch *store.ReleaseBatch) error {
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if err := s.Log.Ensure(ctx, releasesLog); err != nil {
		return err
	}
	day := time.Now().Format("20060102")
	rv, err := s.loadReleases(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, previous := range rv.Batches {
		if previous.Proj == batch.Proj && strings.Contains(previous.ID, day) {
			n++
		}
	}
	batch.ID = fmt.Sprintf("REL-%s-%02d", day, n+1)
	_, err = s.Log.Append(ctx, releasesLog, store.FactReleaseBatch, batch.By, *batch)
	return err
}

func stampNow() string { return time.Now().Format("01-02 15:04") }
