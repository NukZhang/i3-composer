package store

import (
	"encoding/json"
	"fmt"

	"github.com/zhangkun/i3-composer/internal/domain"
)

// View 是投影：把事实日志折叠成一个可渲染的状态。
// 任何时候都可以从日志重放得到同一个 View —— 这是「投影可重建」的含义。
type View struct {
	PBC       string                               `json:"pbc"`
	Name      string                               `json:"name"`
	Kind      string                               `json:"kind"`              // pbc | agent | hybrid
	AgentID   string                               `json:"agentId,omitempty"` // kind != pbc 时关联的 Agent
	Seq       int                                  `json:"seq"`
	Versions  map[domain.Artifact][]domain.Version `json:"versions"`
	Commits   []domain.Commit                      `json:"commits"`
	Tags      []string                             `json:"tags"`
	Builds    map[string]string                    `json:"builds"`   // commitNo → digest
	Binaries  map[string]bool                      `json:"binaries"` // commitNo → 是否产出了可执行文件
	Deploys   []Deploy                             `json:"deploys"`
	Items     map[string]*Item                     `json:"items"`
	Annos     []Anno                               `json:"annos"`
	SvcVerify *domain.SvcVerify                    `json:"svcVerify,omitempty"`

	// Test 是最近一次真跑测试的结果（go test 的真实输出，不是编出来的条数）。
	Test *TestResult `json:"test,omitempty"`
	// Events 是通过 pub/sub 收到的事件：收发闭环的证据。
	Events []EventRecord `json:"events,omitempty"`

	// v2.5/v2.6：真人验收台的清单、判定、bug 与对话。
	Checks       []domain.Check `json:"checks,omitempty"`
	CheckCommit  string         `json:"checkCommit,omitempty"`
	commitChecks []domain.Check
	Defects      []domain.Defect `json:"defects,omitempty"`
	Chats        []VerifyChat    `json:"chats,omitempty"`

	// GateEvals 是历次打 tag 时算出的那组闸。它是「当时」的证据，
	// 不是按今天的账本重算的结果——重算给的永远是现在。
	GateEvals []GateEvaluation `json:"gateEvals,omitempty"`
}

// CommitCreated 将本次人工 AT 清单与制品树原子固化。旧提交仍可解码。
type CommitCreated struct {
	domain.Commit
	Checks []domain.Check `json:"checks,omitempty"`
}

// GateEvaluation 是打 tag 那一刻的判定快照。
//
// 它存在的理由只有一个：tag 是交付凭据，凭据要能自证。
// 只看 tag.created 能回答「打了 tag」，回答不了「当时四闸、验收、测试分别是什么」。
type GateEvaluation struct {
	Tag    string        `json:"tag"`
	Commit string        `json:"commit"`
	Gates  []domain.Gate `json:"gates"`
	Checks CheckSummary  `json:"checks"`
	Test   *TestResult   `json:"test,omitempty"`
	By     string        `json:"by"`
	At     string        `json:"at"`
}

// CheckSummary 是验收清单在打 tag 那一刻的分布。
type CheckSummary struct {
	Total int `json:"total"`
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Todo  int `json:"todo"`
}

// VerifyChat 是验收台里「你来当那个客户」的一段对话。
type VerifyChat struct {
	Who  string `json:"who"` // me | ai
	Text string `json:"t"`
	At   string `json:"at"`
	By   string `json:"by,omitempty"`
}

// TestResult 是一次真跑测试的结果。
type TestResult struct {
	Commit   string   `json:"commit"`
	Pass     int      `json:"pass"`
	Total    int      `json:"total"`
	Failures []string `json:"failures,omitempty"`
	At       string   `json:"at"`
	By       string   `json:"by"`
}

// EventRecord 是一条经 pub/sub 收到的事件。
type EventRecord struct {
	Topic   string         `json:"topic"`
	EventID string         `json:"eventId,omitempty"`
	At      string         `json:"at"`
	PBC     string         `json:"pbc,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type Deploy struct {
	ID     string `json:"id,omitempty"` // 工作项号：让这条部署事实能出现在「这条工作项的账本事实」里
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
	URL    string `json:"url"`
	At     string `json:"at"`
	By     string `json:"by"`
	Status string `json:"status"`
}

type Anno struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Text  string  `json:"text"`
	At    string  `json:"at"`
	By    string  `json:"by"`
	Round string  `json:"round"`
}

// Step 是执行链路的一步。
type Step struct {
	K     string `json:"k"`
	N     string `json:"n"`
	D     string `json:"d"`
	St    string `json:"st"` // todo | doing | done | blocked
	At    string `json:"at,omitempty"`
	Human bool   `json:"human,omitempty"`
}

// Item 是一个工作项（backlog 行）。人和 X worker 同表。
//
// Parent 指向它挂在哪条 Story 下：task 自身没有 pbc 字段的语义负担，
// 归属由 parent 决定（v3.2 的 story / task 分层渲染就靠它）。
type Item struct {
	ID        string `json:"id"`
	Title     string `json:"t"`
	PT        int    `json:"pt"`
	St        string `json:"st"` // todo | doing | done
	Owner     string `json:"owner"`
	Kind      string `json:"kind"` // Story | Task
	PBC       string `json:"pbc"`
	Parent    string `json:"parent,omitempty"`
	Type      string `json:"type"` // new | feature | defect | backlog | doc
	Mode      string `json:"mode"` // fast | guided
	Env       string `json:"env"`
	Run       string `json:"run"` // idea | running | waiting | paused | blocked | published
	Step      int    `json:"step"`
	Steps     []Step `json:"steps,omitempty"`
	Version   string `json:"version,omitempty"`
	Tag       string `json:"tag,omitempty"`
	URL       string `json:"url,omitempty"`
	By        string `json:"by,omitempty"`
	Blocked   string `json:"blocked,omitempty"`
	BlockedBy string `json:"blockedBy,omitempty"`
	At        string `json:"at,omitempty"`

	// 准入输入：建单时由人写下的目标断言、数据范围与责任人。
	//
	// 它们和别的状态一样是事实的一部分（随 item.created 落账），
	// 不是 BFF 的临时变量——否则重启一次，「谁验收、碰哪些表」就没了，
	// 而准入的门槛还在，工作项会变成永远进不去也说不清缺什么的孤儿。
	Accept   []string `json:"accept,omitempty"`   // 验收口径，Given/When/Then
	Data     []string `json:"data,omitempty"`     // 要碰的库表 / 字段
	AcceptBy string   `json:"acceptBy,omitempty"` // 验收人
	ComplyBy string   `json:"complyBy,omitempty"` // 合规背责人

	// Admit 是最近一次入口准入的结论。它不参与 run 状态机——
	// 那是 workflow 的事；这里只回答「进流水线之前判过什么」。
	Admit *AdmitInfo `json:"admit,omitempty"`
}

// AdmitInfo 是入口准入的落账结论。
//
// St 只有 admitted / rejected 两种。admitted 时 Waivers 非空表示「放行但没判全」；
// rejected 时 Reasons 一人一行，是「缺什么 / 找谁 / 补哪」拆开的三段。
type AdmitInfo struct {
	St      string   `json:"st"`
	Waivers []string `json:"waivers,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	By      string   `json:"by,omitempty"`
	At      string   `json:"at,omitempty"`
}

// StepPlan 是交付链路的固定六步。
//
// v3.2 起单元只做到「可以交付」：机器跑完前五步就停在「打 tag」，
// 打 tag 进「发布编排」池；上哪个环境、和谁一起上，在项目级定。
// 部署不再是单元的一步——它属于发布批次。
func StepPlan(env string) []Step {
	return []Step{
		{K: "artifacts", N: "生成制品", D: "X 团队按变更类型产出 PRD / 原型 / AD / AT / Backlog", St: "todo"},
		{K: "converge", N: "收敛过期", D: "按依赖顺序重算，直到工作区干净", St: "todo"},
		{K: "commit", N: "原子提交", D: "整棵树落一个版本号，全落或全不落", St: "todo"},
		{K: "test", N: "测试", D: "单元测试 + 验收用例，全过才放行", St: "todo"},
		{K: "build", N: "构建产物", D: "产出 digest 写回提交", St: "todo"},
		{K: "tag", N: "打 tag 进池", D: "验收全过才打得动；tag 是发布编排唯一认的东西", St: "todo", Human: true},
	}
}

// Project 是真的是纯函数：同样的事实序列，必然得到同样的 View。
// 不读时钟、不读环境、不访问网络。
func Project(pbc string, facts []Fact) (*View, error) {
	v := &View{
		PBC:      pbc,
		Kind:     string(domain.KindPBC),
		Seq:      len(facts),
		Versions: map[domain.Artifact][]domain.Version{},
		Builds:   map[string]string{},
		Binaries: map[string]bool{},
		Items:    map[string]*Item{},
	}
	for _, f := range facts {
		if err := apply(v, f); err != nil {
			return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
		}
	}
	if len(v.commitChecks) > 0 {
		v.Checks = v.commitChecks
		v.CheckCommit = v.Commits[0].No
	}
	return v, nil
}

// UpTo 截出「seq 及之前」的事实窗口。它不排序、不去重、不改写：
// 调用方传入的就是账本顺序（Load 按 seq 升序返回）。
//
// seq <= 0 返回 nil（空账本），而不是偷偷给全量——「指定了一个不存在的
// 时间点」和「什么也没发生」是两件事，调用方要能分得开。
func UpTo(facts []Fact, seq int) []Fact {
	if seq <= 0 {
		return nil
	}
	if seq >= len(facts) {
		// 传的 seq 比账本还长：多半是拿旧账本的 seq 查新账本。
		// 仍然只给已有部分，让投影按真实内容算，不编造。
		return facts[:len(facts)]
	}
	return facts[:seq]
}

// ProjectAsOf 是 Project 的「按时间点」版本：只折叠 seq 及之前的事实。
//
// 它仍然是纯函数：切片是确定的，折叠是确定的，不读时钟也不读环境。
// 与 Project(facts) 的关系只有一条约定：tag 之后没有新增事实时，两者结果一致。
func ProjectAsOf(pbc string, facts []Fact, seq int) (*View, error) {
	return Project(pbc, UpTo(facts, seq))
}

func apply(v *View, f Fact) error {
	dec := func(dst any) error {
		if len(f.Payload) == 0 {
			return nil
		}
		return json.Unmarshal(f.Payload, dst)
	}

	switch f.Type {
	case FactPBCCreated:
		var p struct {
			Name string `json:"name"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		v.Name = p.Name

	case FactArtifactDrafted, FactArtifactAccepted:
		var ver domain.Version
		if err := dec(&ver); err != nil {
			return err
		}
		if ver.T == "" {
			return fmt.Errorf("artifact 事缺少制品类型")
		}
		// 同制品若已有草稿，草稿被新版本取代（superseded），不删。
		arr := v.Versions[ver.T]
		for i := range arr {
			if arr[i].St == domain.StDraft {
				arr[i].St = domain.StSuperseded
			}
		}
		v.Versions[ver.T] = append(arr, ver)

	case FactCommitCreated:
		var c CommitCreated
		if err := dec(&c); err != nil {
			return err
		}
		v.Commits = append([]domain.Commit{c.Commit}, v.Commits...) // 新的在前
		v.commitChecks = c.Checks
		// 提交即接受：把树里引用的版本标为 accepted。
		for _, t := range domain.Order {
			vv := c.Tree[t]
			arr := v.Versions[t]
			for i := range arr {
				if arr[i].V == vv && arr[i].St == domain.StDraft {
					arr[i].St = domain.StAccepted
				}
			}
		}

	case FactTagCreated:
		var p struct {
			Tag string `json:"tag"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		v.Tags = append(v.Tags, p.Tag)
		for i := range v.Commits {
			if v.Commits[i].Tag == p.Tag {
				break
			}
		}
		// tag 挂在最新的那次提交上（由 payload 携带 commit 号更稳）
		var q struct {
			Tag    string `json:"tag"`
			Commit string `json:"commit"`
		}
		if json.Unmarshal(f.Payload, &q) == nil && q.Commit != "" {
			for i := range v.Commits {
				if v.Commits[i].No == q.Commit {
					v.Commits[i].Tag = q.Tag
					break
				}
			}
			// 打过 tag 的那次提交对应的工作项跟着进「已交付」：
			// 工作项是这次交付的入口，tag 是它的结果。
			for _, it := range v.Items {
				if it.Version == q.Commit {
					it.Run = "tagged"
					it.Tag = q.Tag
				}
			}
		}

	case FactGateEvaluated:
		// 只记录「当时算出了什么」，不反过来改任何判定：
		// 闸的结论永远由 domain 现算，这条事实是证据，不是新的真相来源。
		var p GateEvaluation
		if err := dec(&p); err != nil {
			return err
		}
		if p.At == "" {
			p.At = f.At
		}
		if p.By == "" {
			p.By = f.Actor
		}
		v.GateEvals = append(v.GateEvals, p)

	case FactBuildSucceeded:
		var p struct {
			Commit string `json:"commit"`
			Digest string `json:"digest"`
			Binary bool   `json:"binary"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		v.Builds[p.Commit] = p.Digest
		v.Binaries[p.Commit] = p.Binary
		for i := range v.Commits {
			if v.Commits[i].No == p.Commit {
				v.Commits[i].Digest = p.Digest
			}
		}

	case FactDeploySucceeded:
		var d Deploy
		if err := dec(&d); err != nil {
			return err
		}
		v.Deploys = append(v.Deploys, d)

	case FactItemCreated:
		var it Item
		if err := dec(&it); err != nil {
			return err
		}
		if it.ID == "" {
			return fmt.Errorf("item 事缺少 id")
		}
		if it.Steps == nil && it.Kind == "Story" {
			it.Steps = StepPlan(it.Env)
		}
		v.Items[it.ID] = &it

	case FactItemAdmitted:
		// 准入放行。它不改 run（run 是 workflow 的事），只记「判过什么、哪几项没判全」。
		// waivers 为空 = 五项全过；非空 = 软项没过，页面要挂黄条。
		var p struct {
			ID      string   `json:"id"`
			Waivers []string `json:"waivers"`
			By      string   `json:"by"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			by := p.By
			if by == "" {
				by = f.Actor
			}
			it.Admit = &AdmitInfo{St: "admitted", Waivers: p.Waivers, By: by, At: f.At}
		}

	case FactItemAdmitRejected:
		// 被入口拒掉：不产生 item.run.started，也就没有半条链。
		// 这条事实是「为什么没发起」的唯一凭据——说完三行话之后还得能查到。
		var p struct {
			ID      string   `json:"id"`
			Reasons []string `json:"reasons"`
			By      string   `json:"by"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			by := p.By
			if by == "" {
				by = f.Actor
			}
			it.Admit = &AdmitInfo{St: "rejected", Reasons: p.Reasons, By: by, At: f.At}
		}

	case FactItemInputs:
		// 补的是准入输入，不是准入结论：结论只在真正发起时重判一次。
		// 空数组不覆盖已有值——「这次只补了数据范围」不该把责任人清空。
		var p struct {
			ID       string   `json:"id"`
			Accept   []string `json:"accept"`
			Data     []string `json:"data"`
			AcceptBy string   `json:"acceptBy"`
			ComplyBy string   `json:"complyBy"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			if len(p.Accept) > 0 {
				it.Accept = p.Accept
			}
			if len(p.Data) > 0 {
				it.Data = p.Data
			}
			if p.AcceptBy != "" {
				it.AcceptBy = p.AcceptBy
			}
			if p.ComplyBy != "" {
				it.ComplyBy = p.ComplyBy
			}
		}

	case FactItemRunStarted:
		var p struct {
			ID string `json:"id"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			// 发起 = 开新一轮，不是「从上次断点续跑」：步骤状态、上一轮的发布结果
			// （tag / version / url）和阻塞原因全部清空。
			// 否则会出现 step=0 但六步仍标 done、或 run=running 却挂着旧 tag 的矛盾投影。
			it.Run = "running"
			it.St = "doing"
			it.Step = 0
			it.Tag, it.Version, it.URL = "", "", ""
			it.Blocked, it.BlockedBy = "", ""
			for i := range it.Steps {
				it.Steps[i].St = "todo"
				it.Steps[i].At = ""
			}
		}

	case FactItemStepDone:
		var p struct {
			ID   string `json:"id"`
			Step int    `json:"step"`
			At   string `json:"at"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil && p.Step >= 0 && p.Step < len(it.Steps) {
			it.Steps[p.Step].St = "done"
			it.Steps[p.Step].At = p.At
			it.Step = p.Step + 1
		}

	case FactItemWaiting:
		var p struct {
			ID string `json:"id"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			it.Run = "waiting"
		}

	case FactItemPublished:
		var p struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Tag     string `json:"tag"`
			URL     string `json:"url"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			it.Run = "published"
			it.St = "done"
			it.Version, it.Tag, it.URL = p.Version, p.Tag, p.URL
		}

	case FactItemVerified:
		// 机器五步跑完：能交付了，但还没打 tag（没进发布编排池）。
		var p struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			it.Run = "verified"
			it.St = "done"
			if p.Version != "" {
				it.Version = p.Version
			}
			if n := len(it.Steps); n > 0 {
				it.Step = n - 1
			}
		}

	case FactItemBlocked:
		var p struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
			By     string `json:"by"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil {
			it.Run = "blocked"
			it.Blocked = p.Reason
			it.BlockedBy = p.By
		}

	case FactPublishDecided:
		// 决定本身不改投影：等 workflow 打出 tag、落 item.published 才算数。
		// 这条事实只用于审计「谁在什么时候点了发布」。
		var p struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
			By      string `json:"by"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		if it := v.Items[p.ID]; it != nil && !p.Approve {
			it.Run = "blocked"
			it.Blocked = "发布被拒"
			it.BlockedBy = p.By
		}

	case FactAnnotationAdded:
		var a Anno
		if err := dec(&a); err != nil {
			return err
		}
		v.Annos = append(v.Annos, a)

	case FactRoleSwitch:
		// 不影响投影状态：角色是会话级的，不写进账本。

	case FactUnitCreated:
		// 交付单元构成：pbc / agent / hybrid。一条事实定终身，不允许改——
		// 「同一个 PBC 从 pbc 变 hybrid」在账本里就是两个不同的交付物。
		var p struct {
			Kind    string `json:"kind"`
			AgentID string `json:"agentId"`
			Name    string `json:"name"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		k := domain.ParseKind(p.Kind)
		if !k.Valid() {
			return fmt.Errorf("unit 事的 kind 非法：%q", p.Kind)
		}
		v.Kind, v.AgentID = k.String(), p.AgentID
		if p.Name != "" {
			v.Name = p.Name
		}

	case FactSvcVerified:
		var sv domain.SvcVerify
		if err := dec(&sv); err != nil {
			return err
		}
		v.SvcVerify = &sv

	case FactTestRun:
		var p struct {
			Commit   string   `json:"commit"`
			Pass     int      `json:"pass"`
			Total    int      `json:"total"`
			Failures []string `json:"failures"`
			By       string   `json:"by"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		v.Test = &TestResult{
			Commit: p.Commit, Pass: p.Pass, Total: p.Total,
			Failures: p.Failures, At: f.At, By: p.By,
		}

	case FactBRDSaved:
		// BRD 是项目级制品，不进 PBC 的版本树；这里只保留「最近一次保存」的指针，
		// 完整版本线在 ProjectBRD 里。

	case FactCheckDefined:
		var p struct {
			Items []domain.Check `json:"items"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		// 旧式清单以首次落账时的 HEAD 为归属，后续提交不能沿用。
		if len(v.Checks) == 0 && len(p.Items) > 0 && len(v.Commits) > 0 {
			v.CheckCommit = v.Commits[0].No
		}
		for _, c := range p.Items {
			if c.ST == "" {
				c.ST = domain.CheckTodo
			}
			replaced := false
			for i := range v.Checks {
				if v.Checks[i].ID == c.ID {
					v.Checks[i] = c
					replaced = true
				}
			}
			if !replaced {
				v.Checks = append(v.Checks, c)
			}
		}

	case FactCheckDecided:
		var p struct {
			ID     string `json:"id"`
			ST     string `json:"st"`
			Commit string `json:"commit"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		checks := &v.Checks
		if p.Commit != "" {
			if len(v.Commits) == 0 || v.Commits[0].No != p.Commit {
				break // 并发提交之后落下的旧判定不能改变新 HEAD 的进度。
			}
			if len(v.commitChecks) > 0 {
				checks = &v.commitChecks
			}
		}
		out, err := domain.DecideCheck(*checks, p.ID, p.ST)
		if err != nil {
			return err
		}
		*checks = out

	case FactDefect:
		var d domain.Defect
		if err := dec(&d); err != nil {
			return err
		}
		if d.PBC == "" {
			d.PBC = v.PBC
		}
		v.Defects = append(v.Defects, d)

	case FactVerifyChat:
		var p VerifyChat
		if err := dec(&p); err != nil {
			return err
		}
		v.Chats = append(v.Chats, p)
	}
	return nil
}
