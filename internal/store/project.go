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
	Builds    map[string]string                    `json:"builds"` // commitNo → digest
	Deploys   []Deploy                             `json:"deploys"`
	Items     map[string]*Item                     `json:"items"`
	Annos     []Anno                               `json:"annos"`
	SvcVerify *domain.SvcVerify                    `json:"svcVerify,omitempty"`

	// v2.5/v2.6：真人验收台的清单、判定、bug 与对话。
	Checks  []domain.Check  `json:"checks,omitempty"`
	Defects []domain.Defect `json:"defects,omitempty"`
	Chats   []VerifyChat    `json:"chats,omitempty"`
}

// VerifyChat 是验收台里「你来当那个客户」的一段对话。
type VerifyChat struct {
	Who  string `json:"who"` // me | ai
	Text string `json:"t"`
	At   string `json:"at"`
	By   string `json:"by,omitempty"`
}

type Deploy struct {
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
type Item struct {
	ID        string `json:"id"`
	Title     string `json:"t"`
	PT        int    `json:"pt"`
	St        string `json:"st"` // todo | doing | done
	Owner     string `json:"owner"`
	Kind      string `json:"kind"` // Story | Task
	PBC       string `json:"pbc"`
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
}

// StepPlan 是执行链路的固定七步。publish 是唯一人工闸。
func StepPlan(env string) []Step {
	return []Step{
		{K: "artifacts", N: "生成制品", D: "X 团队按变更类型产出 PRD / 原型 / AD / AT / Backlog", St: "todo"},
		{K: "converge", N: "收敛过期", D: "按依赖顺序重算，直到工作区干净", St: "todo"},
		{K: "commit", N: "原子提交", D: "整棵树落一个版本号，全落或全不落", St: "todo"},
		{K: "test", N: "测试", D: "单元测试 + UI 还原，全过才放行", St: "todo"},
		{K: "build", N: "构建镜像", D: "产出 digest 写回提交", St: "todo"},
		{K: "deploy", N: "部署 " + env, D: "先上非生产环境，跑通再说", St: "todo"},
		{K: "publish", N: "发布生产", D: "全链唯一需要人拍板的一步", St: "todo", Human: true},
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
		Items:    map[string]*Item{},
	}
	for _, f := range facts {
		if err := apply(v, f); err != nil {
			return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
		}
	}
	return v, nil
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
		var c domain.Commit
		if err := dec(&c); err != nil {
			return err
		}
		v.Commits = append([]domain.Commit{c}, v.Commits...) // 新的在前
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
		}

	case FactBuildSucceeded:
		var p struct {
			Commit string `json:"commit"`
			Digest string `json:"digest"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		v.Builds[p.Commit] = p.Digest
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
			ID string `json:"id"`
			ST string `json:"st"`
		}
		if err := dec(&p); err != nil {
			return err
		}
		out, err := domain.DecideCheck(v.Checks, p.ID, p.ST)
		if err != nil {
			return err
		}
		v.Checks = out

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
