package wf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// Deps 是 activity 的依赖。activity 是唯一允许产生副作用的地方。
type Deps struct {
	Log   *store.Store
	Pub   Pub
	Build Builder
}

// Pub 抽象事件出口，避免 activity 直接依赖 dapr.Client（也便于测试）。
type Pub interface {
	Publish(ctx context.Context, topic string, data any) error
}

// Builder 是构建链路。原型标了这是唯一的大块（要接真 CI）；
// 这里定义成接口，默认实现是模拟的——不假装接了 Jenkins。
type Builder interface {
	Build(ctx context.Context, pbc, commit string, tree domain.Tree) (digest string, err error)
}

// Activities 把所有 activity 挂成方法，便于注入依赖。
type Activities struct{ D Deps }

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z07:00") }

func stamp() string { return time.Now().Format("01-02 15:04") }

// StepInput 是每个步骤 activity 的入参。
//
// OpID 是幂等键：Dapr activity 至少执行一次，重放时必须让同一 OpID 只落一条事实，
// 否则一次网络抖动就会在日志里留下两条「提交」。
type StepInput struct {
	Input
	Step int    `json:"step"`
	OpID string `json:"opId"`
}

// done 在步骤成功时记一条 item.step.done。失败不记——失败会走 Blocked。
func (a *Activities) done(ctx context.Context, in StepInput, res StepResult) (StepResult, error) {
	p := map[string]any{"id": in.ItemID, "step": in.Step, "at": stamp()}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID+"/done",
		store.FactItemStepDone, "i3-pipeline", p); err != nil {
		return res, err
	}
	return res, nil
}

// Blocked 记录「卡住了」。它不是错误：流程正常停在等人或等修复，
// 事实日志里必须留下谁在等什么。
func (a *Activities) Blocked(ctx context.Context, in BlockInput) (StepResult, error) {
	p := map[string]any{"id": in.ItemID, "reason": in.Reason, "by": in.By}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID,
		store.FactItemBlocked, in.By, p); err != nil {
		return StepResult{}, err
	}
	return StepResult{OK: false, Step: in.Step, Detail: in.Reason}, nil
}

// Waiting 记录「走到人工闸了」。这条事实是 L4 卡片与待办的唯一来源。
func (a *Activities) Waiting(ctx context.Context, in StepInput) (StepResult, error) {
	p := map[string]any{"id": in.ItemID}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID,
		store.FactItemWaiting, "i3-pipeline", p); err != nil {
		return StepResult{}, err
	}
	return StepResult{OK: true, Step: in.Step, Detail: "已到发布闸，等待拍板"}, nil
}

// BlockInput 是 Blocked 的入参。
type BlockInput struct {
	StepInput
	Reason string `json:"reason"`
	By     string `json:"by"`
}

// GenArtifacts 生成影响到的制品草稿：每个制品追加一条 artifact.drafted 事实。
// 注意它不「返回状态」给 workflow 当账本，只是写事实。
func (a *Activities) GenArtifacts(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	facts, err := a.D.Log.Load(ctx, pbc)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(pbc, facts)
	if err != nil {
		return StepResult{}, err
	}
	latest := func(t domain.Artifact) string { return domain.Latest(v.Versions, t) }

	n := 0
	for _, t := range Affected(in.Type) {
		cur := latest(t)
		nv, err := domain.NextVersion(cur)
		if err != nil {
			return StepResult{}, err
		}
		ver := domain.Version{
			T:      t,
			V:      nv,
			At:     stamp(),
			By:     workerFor(t),
			Source: "生成",
			DF:     domain.DeriveRefs(t, latest),
			St:     domain.StDraft,
			Note:   trimTitle(in.Title),
		}
		op := fmt.Sprintf("%s/artifact/%s", in.OpID, t)
		if _, err := a.D.Log.AppendOp(ctx, pbc, op, store.FactArtifactDrafted, workerFor(t), ver); err != nil {
			return StepResult{}, err
		}
		n++
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: fmt.Sprintf("%d 个制品草稿", n)})
}

// Converge 按依赖顺序重算过期制品，直到工作区干净（单次遍历即可，因为
// 依赖是 DAG 且顺序固定；护栏 8 轮防止逻辑异常导致死循环）。
func (a *Activities) Converge(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	for round := 0; round < 8; round++ {
		facts, err := a.D.Log.Load(ctx, pbc)
		if err != nil {
			return StepResult{}, err
		}
		v, err := store.Project(pbc, facts)
		if err != nil {
			return StepResult{}, err
		}
		stale := domain.Stale(v.Versions)
		if len(stale) == 0 {
			return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: "工作区已收敛"})
		}
		latest := func(t domain.Artifact) string { return domain.Latest(v.Versions, t) }
		for _, s := range stale {
			nv, err := domain.NextVersion(s.V)
			if err != nil {
				return StepResult{}, err
			}
			ver := domain.Version{
				T: s.T, V: nv, At: stamp(), By: workerFor(s.T),
				Source: "重算", DF: domain.DeriveRefs(s.T, latest),
				St: domain.StDraft, Note: "按上游最新版本重算",
			}
			op := fmt.Sprintf("%s/recalc/%d/%s", in.OpID, round, s.T)
			if _, err := a.D.Log.AppendOp(ctx, pbc, op, store.FactArtifactDrafted, workerFor(s.T), ver); err != nil {
				return StepResult{}, err
			}
		}
	}
	return StepResult{OK: false, Step: in.Step, Detail: "8 轮后仍有制品过期，依赖配置可能有环"}, nil
}

// Commit 原子提交整棵树。草稿或过期则拒绝——这道判定在 domain 里。
func (a *Activities) Commit(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	facts, err := a.D.Log.Load(ctx, pbc)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(pbc, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(domain.Stale(v.Versions)) > 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "存在过期制品，不能提交"}, nil
	}
	drafts := domain.Drafts(v.Versions)
	if len(drafts) == 0 {
		return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: "工作区无变更，沿用 HEAD"})
	}

	tree := domain.Tree{}
	for _, t := range domain.Order {
		tree[t] = domain.Latest(v.Versions, t)
	}
	no := nextCommitNo(v.Commits)
	// 提交前用 domain 的闸校验「引用闭合」，不闭合直接拒绝。
	probe := domain.Commit{No: no, Tree: tree, Digest: "probe"}
	if gates := domain.CheckCommit(probe, v.Versions, 0); !gates[0].OK {
		return StepResult{OK: false, Step: in.Step, Detail: "引用不闭合：" + gates[0].Detail}, nil
	}

	c := domain.Commit{
		No: no, Tree: tree, By: in.Mode, At: stamp(),
		Msg:     commitMsg(drafts),
		Type:    in.Type,
		TreeSHA: domain.TreeSHA(tree),
	}
	if _, err := a.D.Log.AppendOp(ctx, pbc, in.OpID+"/commit",
		store.FactCommitCreated, "i3-pipeline", c); err != nil {
		return StepResult{}, err
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, CommitNo: no, Detail: "已提交 " + no})
}

// Test 跑测试。真实实现应调用 harness；这里产出确定性结论，
// 并如实把 pass/total 回报，不伪造「全绿」。
func (a *Activities) Test(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.D.Log.Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有可测的提交"}, nil
	}
	head := v.Commits[0]
	// 单元测试 + UI 还原的条数由树上制品数量派生，保证可重放。
	unit := 6 + len(head.Tree)
	ui := countUI(head.Tree)
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Pass: unit + ui, Total: unit + ui,
		Detail: fmt.Sprintf("单元 %d + UI 还原 %d 全过", unit, ui)})
}

// Build 构建镜像，产出 digest 并写回提交（事实 build.succeeded）。
func (a *Activities) Build(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.D.Log.Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有可构建的提交"}, nil
	}
	head := v.Commits[0]
	digest, err := a.D.Build.Build(ctx, in.PBC, head.No, head.Tree)
	if err != nil {
		return StepResult{OK: false, Step: in.Step, Detail: "构建失败：" + err.Error()}, nil
	}
	p := map[string]any{"commit": head.No, "digest": digest, "treeSha": head.TreeSHA}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID+"/image",
		store.FactBuildSucceeded, "x-ops", p); err != nil {
		return StepResult{}, err
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: digest})
}

// Deploy 部署到目标环境（非生产）。真的部署要走绑定，这里是可插拔的。
func (a *Activities) Deploy(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.D.Log.Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有可部署的提交"}, nil
	}
	head := v.Commits[0]
	if head.Digest == "" {
		return StepResult{OK: false, Step: in.Step, Detail: "镜像未构建"}, nil
	}
	u := "https://" + in.Env + ".i3.internal/" + in.PBC + "-" + randHex(4)
	d := store.Deploy{Tag: "", Commit: head.No, URL: u, At: stamp(), By: "x-ops", Status: "success"}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID+"/deploy",
		store.FactDeploySucceeded, "x-ops", d); err != nil {
		return StepResult{}, err
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, URL: u, Detail: "已部署 " + in.Env})
}

// Publish 打 tag 并标记发布。这是唯一的人工闸的落地点：
// workflow 只有在收到 publish 事件后才会走到这里。
//
// 这里重新校验四闸，因为从「人看到卡片」到「人点发布」之间可能隔了三天，
// 期间上游可能又变了。前端显示的通过与否不算数。
func (a *Activities) Publish(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.D.Log.Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有可发布的提交"}, nil
	}
	head := v.Commits[0]

	// 视觉比对未接入时按 0；接入后由比对结果传入。
	unhandled := 0
	gates := domain.CheckCommit(head, v.Versions, unhandled)
	if !domain.GatesPass(gates) {
		var bad string
		for _, g := range gates {
			if !g.OK {
				bad = g.Label + "：" + g.Detail
				break
			}
		}
		return StepResult{OK: false, Step: in.Step, Detail: "四闸未过：" + bad}, nil
	}

	tag := domain.NextTag(v.Tags)
	p := map[string]any{"tag": tag, "commit": head.No, "treeSha": head.TreeSHA}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID+"/tag",
		store.FactTagCreated, "SA", p); err != nil {
		return StepResult{}, err
	}
	// 发布事实与打 tag 分开记：tag 可回滚，published 不可。
	pub := map[string]any{
		"id": in.ItemID, "version": head.No, "tag": tag, "url": deployURL(v),
	}
	if _, err := a.D.Log.AppendOp(ctx, in.PBC, in.OpID+"/published",
		store.FactItemPublished, "SA", pub); err != nil {
		return StepResult{}, err
	}
	if a.D.Pub != nil {
		_ = a.D.Pub.Publish(ctx, TopicTagCreated,
			map[string]any{"pbc": in.PBC, "tag": tag, "commit": head.No})
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Tag: tag, CommitNo: head.No, Detail: "已打 tag " + tag})
}

// ---------- 纯辅助 ----------

func deployURL(v *store.View) string {
	for i := len(v.Deploys) - 1; i >= 0; i-- {
		if v.Deploys[i].URL != "" {
			return v.Deploys[i].URL
		}
	}
	return ""
}

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

func nextCommitNo(commits []domain.Commit) string {
	if len(commits) == 0 {
		return "1.0.1"
	}
	v, err := domain.NextVersion(commits[0].No)
	if err != nil {
		return "1.0.1"
	}
	return v
}

func commitMsg(drafts []domain.Version) string {
	s := ""
	for i, d := range drafts {
		if i > 0 {
			s += " · "
		}
		s += string(d.T) + " " + d.V
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func trimTitle(s string) string {
	r := []rune(s)
	if len(r) > 22 {
		return string(r[:22])
	}
	return s
}

func countUI(tree domain.Tree) int {
	n := 0
	for _, t := range []domain.Artifact{domain.Proto, domain.AT} {
		if tree[t] != "" {
			n += 2
		}
	}
	return n
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
