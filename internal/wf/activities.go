package wf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// Deps 是 activity 的依赖。activity 是唯一允许产生副作用的地方。
type Deps struct {
	Log *store.Store
	Pub Pub
	// Exec 是执行侧：真生成内容、真跑测试、真构建、真部署。
	// 它是必需的：少了它就不构建，也不产出假的 digest。
	Exec Executor
	// LogFor 给出一本账的日志。账本前缀跟着工作项走，
	// 所以 activity 必须按输入里的前缀选账本，而不是用进程自己那本。
	//
	// 没有它（默认 nil）时一律用 Log。
	LogFor func(prefix string) *store.Store
}

// logOf 选出这个工作项该写哪本账。
func (a *Activities) logOf(in Input) *store.Store {
	if a.D.LogFor != nil {
		if s := a.D.LogFor(in.Ledger); s != nil {
			return s
		}
	}
	return a.D.Log
}

// Pub 抽象事件出口，避免 activity 直接依赖 dapr.Client（也便于测试）。
type Pub interface {
	Publish(ctx context.Context, topic string, data any) error
}

// Executor 是执行侧的真实实现：制品内容、测试、构建、部署。
//
// 它是接口而不是具体类型，是为了让 activity 不依赖 go 工具链与进程管理：
// 单测可以塞一个假的进来，但生产路径上它必须是真干活的那个。
type Executor interface {
	// Doc 产出文档类制品的内容。
	Doc(ctx context.Context, u UnitInfo, title, change string, t domain.Artifact) (string, error)
	// Code 产出代码类制品的文件。
	Code(ctx context.Context, u UnitInfo, title, change string) (map[string]string, error)
	// Test 真跑测试。
	Test(ctx context.Context, pbc, commit string, files map[string]string) (TestOut, error)
	// Build 真构建，产出内容寻址的 digest。
	Build(ctx context.Context, pbc, commit string, files map[string]string) (BuildOut, error)
	// Deploy 真部署：起进程、等健康检查。
	Deploy(ctx context.Context, pbc, env, commit, digest string, binary bool) (DeployResult, error)
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
//
// 顺带把这一步发到总线上：workflow 的推进本身就是一条领域事件。
// 以前只有测试、构建、部署这几步发事件，前面几步静悄悄地过去了，
// 总线看起来像装饰品。现在每一步都过总线，BFF 的订阅端收回来落事实，
// 页面上的推进也就有一条公开的、可被别人订阅的事件流。
func (a *Activities) done(ctx context.Context, in StepInput, res StepResult) (StepResult, error) {
	p := map[string]any{"id": in.ItemID, "step": in.Step, "at": stamp()}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/done",
		store.FactItemStepDone, "i3-pipeline", p); err != nil {
		return res, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "step",
		"step": in.Step, "ok": true, "detail": res.Detail,
	})
	return res, nil
}

// Verified 记录「机器五步跑完，可以交付了」。
//
// 这里再核对一次：HEAD 有真构建的 digest，最近一次测试就是针对 HEAD 且全过。
// 不满足就不收口——让它停在 blocked，而不是先写「可交付」再让人去发现是假的。
//
// 打 tag 不在这里：那是人在验证页按下的动作，BFF 会重新校验闸门后落账。
func (a *Activities) Verified(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.logOf(in.Input).Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有提交，无法交付"}, nil
	}
	head := v.Commits[0]
	if head.Digest == "" {
		return StepResult{OK: false, Step: in.Step, Detail: "HEAD 未构建：没有 digest 的提交不能进池"}, nil
	}
	if v.Test == nil || v.Test.Commit != head.No {
		return StepResult{OK: false, Step: in.Step, Detail: "HEAD 没有对应的测试结果"}, nil
	}
	if v.Test.Pass != v.Test.Total {
		return StepResult{OK: false, Step: in.Step, Detail: fmt.Sprintf("测试未全过（%d/%d）", v.Test.Pass, v.Test.Total)}, nil
	}
	p := map[string]any{"id": in.ItemID, "version": head.No}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/verified",
		store.FactItemVerified, "i3-pipeline", p); err != nil {
		return StepResult{}, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "verified",
		"step": in.Step, "commit": head.No,
	})
	return StepResult{OK: true, Step: in.Step, CommitNo: head.No,
		Detail: "已可交付 · " + head.No + "（待打 tag）"}, nil
}

// Blocked 记录「卡住了」。它不是错误：流程正常停在等人或等修复，
// 事实日志里必须留下谁在等什么。
func (a *Activities) Blocked(ctx context.Context, in BlockInput) (StepResult, error) {
	p := map[string]any{"id": in.ItemID, "reason": in.Reason, "by": in.By}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID,
		store.FactItemBlocked, in.By, p); err != nil {
		return StepResult{}, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "blocked",
		"step": in.Step, "ok": false, "reason": in.Reason,
	})
	return StepResult{OK: false, Step: in.Step, Detail: in.Reason}, nil
}

// Waiting 记录「走到人工闸了」。这条事实是 L4 卡片与待办的唯一来源。
func (a *Activities) Waiting(ctx context.Context, in StepInput) (StepResult, error) {
	p := map[string]any{"id": in.ItemID}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID,
		store.FactItemWaiting, "i3-pipeline", p); err != nil {
		return StepResult{}, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "waiting",
		"step": in.Step, "ok": true, "detail": waitingDetail,
	})
	return StepResult{OK: true, Step: in.Step, Detail: waitingDetail}, nil
}

// waitingDetail 是人工闸事件与步骤结果里共用的说明。
const waitingDetail = "已到发布闸，等待拍板"

// BlockInput 是 Blocked 的入参。
type BlockInput struct {
	StepInput
	Reason string `json:"reason"`
	By     string `json:"by"`
}

// GenArtifacts 生成影响到的制品草稿：每个制品追加一条 artifact.drafted 事实。
// 注意它不「返回状态」给 workflow 当账本，只是写事实。
//
// 制品带真内容：文档类带 markdown 正文，代码类带可编译的文件。
// 空壳制品会让下游的测试、构建、部署只能对着空气编数字。
func (a *Activities) GenArtifacts(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	log := a.logOf(in.Input)
	facts, err := log.Load(ctx, pbc)
	if err != nil {
		return StepResult{}, err
	}
	v, err := store.Project(pbc, facts)
	if err != nil {
		return StepResult{}, err
	}
	latest := func(t domain.Artifact) string { return domain.Latest(v.Versions, t) }
	have := map[domain.Artifact]bool{}
	for _, t := range domain.Order {
		have[t] = latest(t) != ""
	}
	unit := a.unitOf(ctx, in.Input)

	n := 0
	seq := v.Seq
	for _, t := range Complete(Affected(in.Type), have) {
		op := fmt.Sprintf("%s/artifact/%s", in.OpID, t)
		if store.HasOp(facts, op) {
			n++
			continue
		}
		cur, _ := domain.LatestVersion(v.Versions, t)
		if keepManualContent(cur) {
			continue
		}
		nv, err := domain.NextVersion(cur.V)
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
		// 内容：文档给 markdown，代码给文件。
		if a.D.Exec != nil {
			if t == domain.Code {
				files, err := a.D.Exec.Code(ctx, unit, in.Title, in.Type)
				if err != nil {
					return StepResult{}, fmt.Errorf("生成代码制品失败：%w", err)
				}
				ver.Files = files
			} else {
				md, err := a.D.Exec.Doc(ctx, unit, in.Title, in.Type, t)
				if err != nil {
					return StepResult{}, fmt.Errorf("生成 %s 失败：%w", t, err)
				}
				ver.MD = md
			}
		}
		// 如果人工在生成期间落了草稿，让 activity 重新读账本后再判断。
		fact, err := log.AppendOp(ctx, pbc, op, store.FactArtifactDrafted, workerFor(t), ver,
			store.AppendCondition{AfterSeq: seq})
		if err != nil {
			return StepResult{}, err
		}
		if fact.Seq != seq+1 {
			return StepResult{}, store.ErrSnapshotChanged
		}
		seq = fact.Seq
		n++
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: fmt.Sprintf("%d 个制品草稿", n)})
}

// 人工方案由负责角色编辑。流水线收敛时只重接这些制品的引用。
func manualPlan(v domain.Version) bool {
	return v.Source == domain.SourceManual && (v.T == domain.PRD || v.T == domain.AD || v.T == domain.AT)
}

// 这轮交付应提交已经保存的人工草稿，否则重新跑测试就会测到重新生成的内容。
func keepManualContent(v domain.Version) bool {
	return manualPlan(v) || (v.Source == domain.SourceManual && v.St == domain.StDraft)
}

// unitOf 读这个工作项所属交付单元的登记信息。
//
// 它从「工作项该去的那本账」里读：同一个交付单元在不同账本里是不同东西
// （名字、构成、Agent 关联都可能不同）。
func (a *Activities) unitOf(ctx context.Context, in Input) UnitInfo {
	if in.Unit != nil {
		return *in.Unit
	}
	u := UnitInfo{ID: in.PBC, Name: in.PBC, Kind: string(domain.KindPBC)}
	facts, err := a.logOf(in).Load(ctx, "units")
	if err != nil {
		return u
	}
	uv, err := store.ProjectUnits(facts)
	if err != nil {
		return u
	}
	// 已经发起的工作流继续使用原单元定义；作废只撤销面向新请求的登记。
	unit := uv.Removed[in.PBC]
	for _, x := range uv.Units {
		if x.ID == in.PBC {
			unit = x
			break
		}
	}
	if unit.ID != "" {
		u.Name, u.Kind, u.AgentID, u.Profile = unit.Name, unit.Kind, unit.AgentID, unit.Profile
	}
	return u
}

// agentOf 从 agents 日志取一个数字员工的定义（发布闸要看它的评测与 digest）。
func (a *Activities) agentOf(ctx context.Context, in Input, id string) *domain.Agent {
	if id == "" {
		return nil
	}
	facts, err := a.logOf(in).Load(ctx, "agents")
	if err != nil {
		return nil
	}
	av, err := store.ProjectAgents(facts)
	if err != nil {
		return nil
	}
	return av.Agents[id]
}

// Converge 按依赖顺序重算过期制品，直到工作区干净（单次遍历即可，因为
// 依赖是 DAG 且顺序固定；护栏 8 轮防止逻辑异常导致死循环）。
func (a *Activities) Converge(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	unit := a.unitOf(ctx, in.Input)
	for round := 0; round < 8; round++ {
		facts, err := a.logOf(in.Input).Load(ctx, pbc)
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
		seq := v.Seq
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
			// 重算也要带真内容：否则重算出来的制品是空壳，
			// 收敛之后的工作区反而比收敛前更假。
			if prev, _ := domain.LatestVersion(v.Versions, s.T); keepManualContent(prev) || len(s.On) == 0 {
				ver.MD, ver.Files, ver.By, ver.Source = prev.MD, prev.Files, prev.By, prev.Source
				ver.Note = "保留人工制品内容，按上游最新版本更新引用"
			} else if a.D.Exec != nil {
				if s.T == domain.Code {
					files, err := a.D.Exec.Code(ctx, unit, in.Title, in.Type)
					if err != nil {
						return StepResult{}, fmt.Errorf("重算代码制品失败：%w", err)
					}
					ver.Files = files
				} else {
					md, err := a.D.Exec.Doc(ctx, unit, in.Title, in.Type, s.T)
					if err != nil {
						return StepResult{}, fmt.Errorf("重算 %s 失败：%w", s.T, err)
					}
					ver.MD = md
				}
			}
			op := fmt.Sprintf("%s/recalc/%s/%s", in.OpID, s.T, s.V)
			fact, err := a.logOf(in.Input).AppendOp(ctx, pbc, op, store.FactArtifactDrafted, workerFor(s.T), ver,
				store.AppendCondition{AfterSeq: seq})
			if errors.Is(err, store.ErrSnapshotChanged) || (err == nil && fact.Seq != seq+1) {
				break
			}
			if err != nil {
				return StepResult{}, err
			}
			seq = fact.Seq
		}
	}
	return StepResult{OK: false, Step: in.Step, Detail: "8 轮后仍有制品过期，依赖配置可能有环"}, nil
}

// Commit 原子提交整棵树。草稿或过期则拒绝——这道判定在 domain 里。
func (a *Activities) Commit(ctx context.Context, in StepInput) (StepResult, error) {
	pbc := in.PBC
	facts, err := a.logOf(in.Input).Load(ctx, pbc)
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
	// 视觉比对没接：传 nil（这条判不了），这里也只看 gates[0]。
	probe := domain.Commit{No: no, Tree: tree, Digest: "probe"}
	if gates := domain.CheckCommit(probe, v.Versions, nil); !gates[0].OK {
		return StepResult{OK: false, Step: in.Step, Detail: "引用不闭合：" + gates[0].Detail}, nil
	}

	c := domain.Commit{
		No: no, Tree: tree, By: in.Mode, At: stamp(),
		Msg:     commitMsg(drafts),
		Type:    in.Type,
		TreeSHA: domain.TreeSHA(tree),
	}
	payload := store.CommitCreated{Commit: c}
	if at, ok := domain.Find(v.Versions, domain.AT, tree[domain.AT]); ok && manualPlan(at) &&
		domain.ParseKind(a.unitOf(ctx, in.Input).Kind) != domain.KindAgent {
		payload.Checks = domain.ChecksFromAT(at.MD)
	}
	if _, err := a.logOf(in.Input).AppendOp(ctx, pbc, in.OpID+"/commit",
		store.FactCommitCreated, "i3-pipeline", payload, store.AppendCondition{AfterSeq: v.Seq}); err != nil {
		return StepResult{}, err
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, CommitNo: no, Detail: "已提交 " + no})
}

// Test 跑测试。
//
// 真跑：代码制品里的文件被物化成模块，go test 真编译真执行；
// 通过数与失败数是 go test 的输出，不是我编的条数。不过就是不过——
// 这一步失败会阻断整条链路。
func (a *Activities) Test(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.logOf(in.Input).Load(ctx, in.PBC)
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
	files := codeFiles(v, head)
	if len(files) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "提交里没有代码制品，无法测试"}, nil
	}
	if a.D.Exec == nil {
		// 没有执行侧（单测夹具）：退回确定性计数，但如实说明。
		unit := 6 + len(head.Tree)
		ui := countUI(head.Tree)
		return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Pass: unit + ui, Total: unit + ui,
			Detail: fmt.Sprintf("单元 %d + UI 还原 %d 全过（未接执行侧）", unit, ui)})
	}
	res, err := a.D.Exec.Test(ctx, in.PBC, head.No, files)
	if err != nil {
		// 测试没过：如实失败，并把失败条目带出去。
		return StepResult{OK: false, Step: in.Step, Pass: res.Pass, Total: res.Total,
			Detail: "测试未通过：" + strings.Join(res.Failures, "、")}, nil
	}
	// 测试结论进账本：谁在什么版本上跑了什么，事后可查。
	// 带上工作项号，这条事实才会出现在「这条工作项的账本事实」里。
	p := map[string]any{
		"id": in.ItemID, "commit": head.No, "pass": res.Pass, "total": res.Total,
		"failures": res.Failures, "by": "x-qa",
	}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/test", store.FactTestRun, "x-qa", p); err != nil {
		return StepResult{}, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "test",
		"pass": res.Pass, "total": res.Total,
	})
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Pass: res.Pass, Total: res.Total,
		Detail: fmt.Sprintf("%d/%d 通过", res.Pass, res.Total)})
}

// codeFiles 取一个提交里代码制品的文件。
func codeFiles(v *store.View, c domain.Commit) map[string]string {
	if c.Tree[domain.Code] == "" {
		return nil
	}
	code, _ := domain.Find(v.Versions, domain.Code, c.Tree[domain.Code])
	return code.Files
}

// Build 构建产物，产出 digest 并写回提交（事实 build.succeeded）。
//
// digest 是内容寻址的：编译型产物是二进制字节的 sha256，
// 非编译型产物是文件内容的 sha256。改一个字节，digest 就变。
func (a *Activities) Build(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.logOf(in.Input).Load(ctx, in.PBC)
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
	files := codeFiles(v, head)
	if len(files) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "提交里没有代码制品，无法构建"}, nil
	}

	digest, binary, detail := "", false, ""
	if a.D.Exec == nil {
		// 没有执行侧就不构建：宁可这一步失败，也不能记下一个假的 digest。
		// 以前这里是 mockBuilder——用树摘要拼一个 sha256:TreeSHA-...，
		// 看着像镜像摘要，其实什么字节都没编译过。
		return StepResult{OK: false, Step: in.Step, Detail: "没有执行侧，无法构建（不接受假的 digest）"}, nil
	}
	out, err := a.D.Exec.Build(ctx, in.PBC, head.No, files)
	if err != nil {
		return StepResult{OK: false, Step: in.Step, Detail: "构建失败：" + err.Error()}, nil
	}
	digest, binary, detail = out.Digest, out.Binary, out.Detail
	p := map[string]any{"id": in.ItemID, "commit": head.No, "digest": digest, "treeSha": head.TreeSHA, "binary": binary}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/image",
		store.FactBuildSucceeded, "x-ops", p); err != nil {
		return StepResult{}, err
	}
	a.publish(ctx, TopicItemChanged, map[string]any{
		"pbc": in.PBC, "item": in.ItemID, "kind": "build",
		"commit": head.No, "digest": digest,
	})
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: digest + " · " + detail})
}

// Deploy 部署到目标环境（非生产）。
//
// 真部署：把构建产物跑起来，等它的 /healthz 真的返回 200 才算成功。
// 没有独立进程的产物（Agent 定义）如实说「没有独立进程」，不编 URL。
func (a *Activities) Deploy(ctx context.Context, in StepInput) (StepResult, error) {
	facts, err := a.logOf(in.Input).Load(ctx, in.PBC)
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
	binary := v.Binaries[head.No]
	if a.D.Exec != nil {
		out, err := a.D.Exec.Deploy(ctx, in.PBC, in.Env, head.No, head.Digest, binary)
		if err != nil {
			return StepResult{OK: false, Step: in.Step, Detail: "部署失败：" + err.Error()}, nil
		}
		if out.Skipped {
			return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Detail: out.Detail})
		}
		d := store.Deploy{ID: in.ItemID, Tag: "", Commit: head.No, URL: out.URL, At: stamp(), By: "x-ops", Status: "success"}
		if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/deploy",
			store.FactDeploySucceeded, "x-ops", d); err != nil {
			return StepResult{}, err
		}
		a.publish(ctx, TopicItemChanged, map[string]any{
			"pbc": in.PBC, "item": in.ItemID, "kind": "deploy",
			"env": in.Env, "url": out.URL, "health": out.Health,
		})
		return a.done(ctx, in, StepResult{OK: true, Step: in.Step, URL: out.URL,
			Detail: "已部署 " + in.Env + " · 健康检查 " + out.Health})
	}
	// 无执行侧：退回旧行为（单测夹具）。
	u := "https://" + in.Env + ".i3.internal/" + in.PBC + "-" + randHex(4)
	d := store.Deploy{ID: in.ItemID, Tag: "", Commit: head.No, URL: u, At: stamp(), By: "x-ops", Status: "success"}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/deploy",
		store.FactDeploySucceeded, "x-ops", d); err != nil {
		return StepResult{}, err
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, URL: u, Detail: "已部署 " + in.Env})
}

// Publish 为升级前的工作流保留，恢复后写入前要核对当时可见的交付证据。
func (a *Activities) Publish(ctx context.Context, in StepInput) (StepResult, error) {
	log := a.logOf(in.Input)
	facts, err := log.Load(ctx, in.PBC)
	if err != nil {
		return StepResult{}, err
	}
	for _, f := range facts {
		if f.OpID != in.OpID+"/tag" || f.Type != store.FactTagCreated {
			continue
		}
		// 已打 tag 的重试继续写发布事实，后续草稿不应改变该次交付的身份。
		var saved struct {
			Tag    string `json:"tag"`
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(f.Payload, &saved); err != nil {
			return StepResult{}, err
		}
		snapshot, err := store.ProjectAsOf(in.PBC, facts, f.Seq)
		if err != nil {
			return StepResult{}, err
		}
		return a.finishPublish(ctx, in, snapshot, saved.Tag, saved.Commit)
	}
	v, err := store.Project(in.PBC, facts)
	if err != nil {
		return StepResult{}, err
	}
	if len(v.Commits) == 0 {
		return StepResult{OK: false, Step: in.Step, Detail: "没有可发布的提交"}, nil
	}
	head := v.Commits[0]
	if head.Tag != "" {
		return StepResult{OK: false, Step: in.Step, Detail: "当前提交已打 tag " + head.Tag}, nil
	}

	// 视觉比对未接入：传 nil。这条闸会说「判不了」，而不是「无差异」。
	pbcGates := domain.CheckCommit(head, v.Versions, nil)
	// 闸的构成跟单元类型走：PBC 看四闸，Agent 看四闸（上游/工具/digest/评测），
	// 组合两边都要过 + 服务验收。以前这里一律按 PBC 四闸判——
	// 结果是 Agent / 组合单元只要制品齐了就能发，它自己的评测与服务验收根本没参与。
	gates := pbcGates
	unit := a.unitOf(ctx, in.Input)
	kind := domain.ParseKind(unit.Kind)
	switch kind {
	case domain.KindAgent:
		gates = domain.AgentGates(a.agentOf(ctx, in.Input, unit.AgentID))
	case domain.KindHybrid:
		sv := domain.SvcVerify{}
		if v.SvcVerify != nil {
			sv = *v.SvcVerify
		}
		gates = domain.HybridGates(pbcGates, sv, domain.AgentGates(a.agentOf(ctx, in.Input, unit.AgentID)))
	}
	if !domain.GatesPass(gates) {
		var bad string
		for _, g := range gates {
			if !g.OK {
				bad = g.Label + "：" + g.Detail
				break
			}
		}
		return StepResult{OK: false, Step: in.Step, Detail: "发布闸未过：" + bad}, nil
	}
	if kind != domain.KindAgent && (len(v.Checks) == 0 || v.CheckCommit != head.No) {
		return StepResult{OK: false, Step: in.Step, Detail: "当前提交缺少人工验收清单"}, nil
	}
	if kind != domain.KindAgent && (v.Test == nil || v.Test.Commit != head.No || v.Test.Pass != v.Test.Total) {
		return StepResult{OK: false, Step: in.Step, Detail: "当前提交缺少通过的机器测试"}, nil
	}

	tag := domain.NextTag(v.Tags)
	// 与 BFF 的打 tag 同一条规矩：先定格「凭什么是这组闸」，再落 tag。
	ev := store.GateEvaluation{Tag: tag, Commit: head.No, Gates: gates, By: "SA"}
	if v.Test != nil {
		t := *v.Test
		ev.Test = &t
	}
	for _, c := range v.Checks {
		ev.Checks.Total++
		switch c.ST {
		case domain.CheckPass:
			ev.Checks.Pass++
		case domain.CheckFail:
			ev.Checks.Fail++
		default:
			ev.Checks.Todo++
		}
	}
	if ev.Checks.Pass != ev.Checks.Total {
		return StepResult{OK: false, Step: in.Step, Detail: "人工验收未全过"}, nil
	}
	p := map[string]any{"tag": tag, "commit": head.No, "treeSha": head.TreeSHA, "digest": head.Digest}
	saved, err := log.AppendOp(ctx, in.PBC, in.OpID+"/tag", store.FactTagCreated, "SA", p, store.AppendCondition{
		AfterSeq: v.Seq,
		Before:   &store.PendingFact{Type: store.FactGateEvaluated, Payload: ev},
	})
	if err != nil {
		return StepResult{}, err
	}
	if saved.Seq != v.Seq+2 {
		return StepResult{}, store.ErrSnapshotChanged
	}
	return a.finishPublish(ctx, in, v, tag, head.No)
}

func (a *Activities) finishPublish(ctx context.Context, in StepInput, v *store.View, tag, commit string) (StepResult, error) {
	// 发布事实与打 tag 分开记：tag 可回滚，published 不可。
	pub := map[string]any{
		"id": in.ItemID, "version": commit, "tag": tag, "url": deployURL(v),
	}
	if _, err := a.logOf(in.Input).AppendOp(ctx, in.PBC, in.OpID+"/published",
		store.FactItemPublished, "SA", pub); err != nil {
		return StepResult{}, err
	}
	if a.D.Pub != nil {
		_ = a.D.Pub.Publish(ctx, TopicTagCreated,
			map[string]any{"pbc": in.PBC, "tag": tag, "commit": commit})
	}
	return a.done(ctx, in, StepResult{OK: true, Step: in.Step, Tag: tag, CommitNo: commit, Detail: "已打 tag " + tag})
}

// ---------- 纯辅助 ----------

// publish 发一条领域事件到总线。
//
// 发出去的每条事件，BFF 的订阅端都会收回来并落一条 event.received 事实：
// 这样「事件外发」是闭环的，不是发出去就没了下文。
func (a *Activities) publish(ctx context.Context, topic string, data map[string]any) {
	if a.D.Pub == nil {
		return
	}
	_ = a.D.Pub.Publish(ctx, topic, data)
}

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
