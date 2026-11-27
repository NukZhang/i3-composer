package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// sharedLog 是服务主账本（空前缀）。发布批次是项目级资产、落在主账本上；
// 隔离实例（LEDGER_PREFIX 非空）里 s.Log 是隔离账本，读批次也必须读主账本，
// 否则「刚发的批次查不到」——接口回 201，回滚却 404（#41 自测遇过）。
func (s *Server) sharedLog() *store.Store {
	if s.Log.Prefix() == "" {
		return s.Log
	}
	o := store.New(s.Log.Client(), s.Log.StateStore())
	o.Notify = s.Log.Notify
	return o
}

func (s *Server) loadReleases(ctx context.Context) (*store.ReleasesView, error) {
	facts, err := s.sharedLog().Load(ctx, releasesLog)
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
// 只存计数不存全文：清单本身在 check.defined / check.decided 里，不重复记。
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
// 三层前提，缺一不可：
//  1. HEAD 有真构建的 digest；
//  2. 最近一次测试针对 HEAD 且全过；
//  3. 闸门全过（判不了的不挡，但也不当成通过）。
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
	// 验收清单存在时，它必须先全过：人验过的才算数。
	// 没有清单的单元（新拆出来的）以机器测试与闸门为准——但页面会说清这件事。
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
	Proj    string               `json:"proj"`
	Units   []orchUnit           `json:"units"`
	Batches []store.ReleaseBatch `json:"batches"`
	// Live[i] 是 Batches[i] 的进程是否还活着（任何一个单元地址 /healthz 200 即 true）。
	// 与状态并列而不是塞进状态里：账本记的是「发过什么」，进程是执行侧的事实（#29）。
	Live []bool   `json:"live"`
	Envs []string `json:"envs"`
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
			out.Batches = append(out.Batches, b)
		}
	}
	// 每个批次带一句「进程还在不在」：#29 的现象是旧批次仍显示「已上线」而地址已经死了。
	// 探测并行走。一个批次里只要有一条地址不通，就算「不全在线」：多环境并存时
	// 只看碰巧活着的那个，会把「SIT 已经死了」说成「批次还活着」（#19）。
	lives := make([][]bool, len(out.Batches))
	var wg sync.WaitGroup
	for i := range out.Batches {
		b := &out.Batches[i]
		if b.Status != "active" {
			continue
		}
		lives[i] = make([]bool, len(b.Units))
		for j := range b.Units {
			u := &b.Units[j]
			if u.URL == "" || u.Status == "rolledback" {
				continue
			}
			wg.Add(1)
			go func(i, j int, url string) {
				defer wg.Done()
				lives[i][j] = liveURL(r.Context(), url)
			}(i, j, u.URL)
		}
	}
	wg.Wait()
	out.Live = make([]bool, len(out.Batches))
	for i := range out.Batches {
		if out.Batches[i].Status != "active" {
			continue
		}
		all, any := true, false
		for _, ok := range lives[i] {
			if ok {
				any = true
			} else {
				all = false
			}
		}
		out.Live[i] = any && all
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
	// 单元账本要先存在，后面的部署事实与撤销事实才落得进去。
	for i := range locked {
		if err := s.Log.Ensure(r.Context(), locked[i].ID); err != nil {
			return err
		}
	}
	// 真部署：PBC 产物起进程并等 /healthz；Agent 产物没有进程，如实标 activated。
	//
	// 落账顺序（#41）：**先写批次，再部署，成功一个补一条 progress**。
	// 以前是先部署、全部成功后才写批次：中途失败时前面几条 deploy.succeeded 已经落进单元账本，
	// 却没有任何批次引用它们，进程又被收回——账本上留下一串「success 的死地址」，而且只会累加。
	// 现在失败也有记录：已起的进程逐个收回，每个都补一条 deploy.rolledback，再补一条 release.batch.failed。
	id := s.nextBatchID(r.Context(), proj)
	b := store.ReleaseBatch{
		ID: id, Proj: proj, Env: in.Env, Mode: in.Mode, Units: locked,
		By: string(c.Role), At: stampNow(), Status: "active",
	}
	// 批次在公共账本里：单元自己的账本也要先存在（失败的单元同样要有撤销事实可落）。
	rel := s.sharedLog()
	if err := rel.Ensure(r.Context(), releasesLog); err != nil {
		return err
	}
	if _, err := rel.Append(r.Context(), releasesLog, store.FactReleaseBatch, string(c.Role), b); err != nil {
		return err
	}
	// fail 收尾一次失败：停进程、逐条撤销已落的部署事实、把批次标成 failed，然后把原因报给调用方。
	fail := func(reason string) error {
		for i := range b.Units {
			l := &b.Units[i]
			if l.URL == "" || !l.Binary {
				continue
			}
			s.Pipe.Stop(l.ID, in.Env)
			// 撤销事实落在单元自己的账本里（部署事实就在那儿）；落不进去就如实报，不吞。
			rb := map[string]any{"url": l.URL, "batch": id, "reason": reason, "at": stampNow()}
			if _, err := s.Log.Append(r.Context(), l.ID, store.FactDeployRolledback, string(c.Role), rb); err != nil {
				slog.Warn("发布失败后撤销部署事实没落账", "unit", l.ID, "url", l.URL, "err", err)
			}
			// 头号嫌疑自查：fail 之前同 pbc 的旧 env 进程有没有被收掉。
			// 没收掉的话，新进程可能根本没起在自己的键上（#41 自测遇过：进程还在应答，账本却说是回滚了）。
			if s.Pipe != nil {
				slog.Info("发布失败收尾后核对进程是否还在", "unit", l.ID, "env", in.Env, "alive", s.Pipe.Alive(l.ID, in.Env))
			}
			l.Status = "rolledback"
		}
		if _, err := rel.Append(r.Context(), releasesLog, store.FactReleaseFailed, string(c.Role),
			map[string]any{"id": id, "reason": reason, "at": stampNow()}); err != nil {
			slog.Warn("批次失败事实没落账", "batch", id, "err", err)
		}
		s.publish("releases", "release.batch.failed", map[string]any{"id": id, "proj": proj})
		return fmt.Errorf("%w：批次 %s 没有上线：%s", errBadRequest, id, reason)
	}
	for i := range b.Units {
		l := &b.Units[i]
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
		l.URL, l.Status = out.URL, "deployed"
		// 部署事实落在单元自己的账本里：这条线的「谁在哪个环境、部署了哪个字节」可查。
		// batch 让每条部署都能被某个批次认领——这是 #41 里那些「无批次引用的 success」不再出现的原因。
		d := store.Deploy{ID: "release", Tag: l.Tag, Commit: l.Commit, URL: out.URL, Env: in.Env,
			At: stampNow(), By: string(c.Role), Status: "success", Batch: id}
		if _, err := s.Log.Append(r.Context(), l.ID, store.FactDeploySucceeded, string(c.Role), d); err != nil {
			return fail("部署 " + l.Name + " 已起进程，但部署事实没落进账本：" + err.Error())
		}
		if _, err := rel.Append(r.Context(), releasesLog, store.FactReleaseProgress, string(c.Role),
			map[string]any{"id": id, "unit": l.ID, "url": out.URL, "at": stampNow()}); err != nil {
			// progress 只是「这一批已经上到第几个」，丢了不影响真相（部署事实 + 批次都在）；
			// 但账本不全会让人以为没上，宁可让发布失败、由人决定重发。
			return fail("部署事实已落账，但批次进度没记上：" + err.Error())
		}
	}
	// 上线之后复核一遍：同一单元别的环境还可能也在跑（#19），前一次部署在新进程起来之后
	// 死掉了，这里才看得见。死了的落 deploy.lost——账本不能明明记着 success、
	// 现实里进程已经没了，还一声不吭（宪法 I：事实日志是唯一权威；判不了就说判不了）。
	// 部署器自己看得到的（登记表里那份）不算「lost」：那是正常换版本留下的。
	for i := range locked {
		l := &locked[i]
		if l.URL == "" || !l.Binary || liveURL(r.Context(), l.URL) {
			continue
		}
		if s.Pipe != nil && s.Pipe.Alive(l.ID, in.Env) {
			continue
		}
		reason := "批次上线复核：地址不再应答"
		rb := map[string]any{"url": l.URL, "batch": id, "reason": reason, "at": stampNow()}
		if _, err := s.Log.Append(r.Context(), l.ID, store.FactDeployLost, string(c.Role), rb); err != nil {
			slog.Warn("部署进程不见了，deploy.lost 没落账", "unit", l.ID, "url", l.URL, "err", err)
		}
		l.Status = "lost"
	}
	s.publish("releases", "release.batch.created", map[string]any{"id": id, "proj": proj})
	writeJSON(w, http.StatusCreated, b)
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
	if s.Pipe != nil {
		for _, u := range b.Units {
			if u.URL == "" || !u.Binary {
				continue
			}
			s.Pipe.Stop(u.ID, b.Env)
			// 回滚同样要撤销单元账本里的那条部署事实：否则单元账本永远停在「成功上线」（#41）。
			rb := map[string]any{"url": u.URL, "batch": b.ID, "reason": "整批回滚：" + in.Note, "at": stampNow()}
			if _, err := s.Log.Append(r.Context(), u.ID, store.FactDeployRolledback, string(c.Role), rb); err != nil {
				slog.Error("回滚：撤销事实没落账", "unit", u.ID, "err", err)
				return err
			}
			slog.Info("回滚：撤销事实已落账", "unit", u.ID, "batch", b.ID)
			// 进程账要留痕：回滚之后登记表里还认不认得这个 (pbc, env)，日志里说清楚。
			// 「接口回 200 但进程还在应答」只有靠这一行才能区分是「没收」还是「别的进程在应答」。
			slog.Info("回滚：部署事实已撤销", "unit", u.ID, "env", b.Env, "url", u.URL, "alive", s.Pipe.Alive(u.ID, b.Env))
		}
	}
	// 回滚写两份账：单元账本上的撤销事实（上面）与公共账本上的批次回滚。
	// 公共账本同样要从前缀里找出来——以前拿主账本写，隔离实例上接口回 200 却没落账（#41 自测遇过）。
	rel := s.sharedLog()
	if err := rel.Ensure(r.Context(), releasesLog); err != nil {
		return err
	}
	if _, err := rel.Append(r.Context(), releasesLog, store.FactReleaseRollback, string(c.Role),
		map[string]any{"id": b.ID, "by": string(c.Role), "at": stampNow(), "note": in.Note}); err != nil {
		return err
	}
	s.publish("releases", "release.batch.rolledback", map[string]any{"id": b.ID})
	writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "status": "rolledback"})
	return nil
}

// nextBatchID 生成 REL-YYYYMMDD-NN：同一项目当天按顺序编号。
//
// 取「已有最大号 + 1」而不是「个数 + 1」：失败批次也留在账上了（#41），
// 按个数数会把同一个号发两次，回滚时捞到的是哪个批次就说不清了。
func (s *Server) nextBatchID(ctx context.Context, proj string) string {
	day := time.Now().Format("20060102")
	rv, err := s.loadReleases(ctx)
	max := 0
	if err == nil {
		for _, b := range rv.Batches {
			if b.Proj != proj {
				continue
			}
			if n, err := strconv.Atoi(b.ID[strings.LastIndex(b.ID, "-")+1:]); err == nil && strings.Contains(b.ID, day) && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("REL-%s-%02d", day, max+1)
}

// liveURL 探一次部署地址是否还活着：账本说「部署过这个字节」，进程还在不在是执行侧的事实，
// 两边都要说清楚（宪法 I）。编排页据此区分「在线」与「进程不在」，而不是拿账本当进程真相（#29）。
func liveURL(ctx context.Context, u string) bool {
	if u == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u, "/")+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 800 * time.Millisecond}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<12))
	return resp.StatusCode == http.StatusOK
}

func stampNow() string { return time.Now().Format("01-02 15:04") }
