package store

import (
	"encoding/json"
	"fmt"
)

// 本文件是 v2.7 新增的一层：工作空间 → 项目。
//
// 上一版只有一个写死的项目，所有交付单元都挂在它下面。现在项目是
// 一等公民：可以新建、可以加入已有的、可以不归入任何工作空间。
// 和别的资产一样，它落在账本里（projects 日志），投影是纯函数。
//
// 一层关系：
//
//	工作空间（文件夹，只做归集）
//	  └─ 项目（一份 BRD + 多个交付单元）
//	       └─ 交付单元（PBC / Agent / 组合）
//
// 工作空间不产生制品；BRD 与交付单元永远挂在项目上。

// Workspace 是一个工作空间。
type Workspace struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Use    string `json:"use,omitempty"` // org（按客户/园区）| team（按团队）
	Shared bool   `json:"shared,omitempty"`
}

// ProjectBRDRef 是项目上挂的那份 BRD 的摘要（正文在 BRD 版本线里）。
type ProjectBRDRef struct {
	V    string `json:"v"`
	File string `json:"file"`
	Size string `json:"size,omitempty"`
	At   string `json:"at,omitempty"`
}

// ProjectRow 是一个项目：承接容器。
//
// 名字不叫 Project：那个名字已经给了「重放某本账」的纯函数（project.go）。
type ProjectRow struct {
	ID     string         `json:"id"`
	Ws     string         `json:"ws,omitempty"` // 空 = 未归入工作空间
	Name   string         `json:"name"`
	Owner  string         `json:"owner,omitempty"`
	Shared bool           `json:"shared,omitempty"` // 加入别人的项目：原处不动，只出现在你的侧栏
	BRD    *ProjectBRDRef `json:"brd,omitempty"`
}

// CatalogProject 是「加入已有」里可选的别人的项目。
type CatalogProject struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ws      string `json:"ws"`
	Owner   string `json:"owner"`
	Members int    `json:"members"`
	Note    string `json:"note"`
}

// CatalogSpace 是「加入已有」里可选的工作空间。
type CatalogSpace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Projects int    `json:"projects"`
	Note     string `json:"note"`
}

// Catalog 是「可加入」的候选集。
//
// 它和账本里的工作空间/项目不是一回事：候选是别人那边的东西，
// 加入之后才在你这本账上留下一条 project.joined。
type Catalog struct {
	Projects []CatalogProject `json:"projects"`
	Spaces   []CatalogSpace   `json:"spaces"`
}

// ProjectsView 是 projects 日志的投影。
type ProjectsView struct {
	Seq        int          `json:"seq"`
	Workspaces []Workspace  `json:"workspaces"`
	Projects   []ProjectRow `json:"projects"`
	Catalog    Catalog      `json:"catalog"`
}

// ProjectProjects 重放 projects 日志。
func ProjectProjects(facts []Fact) (*ProjectsView, error) {
	v := &ProjectsView{Seq: len(facts), Catalog: DefaultCatalog()}
	wsSeen := map[string]int{}
	pjSeen := map[string]int{}
	for _, f := range facts {
		switch f.Type {
		case FactWorkspaceCreated, FactWorkspaceJoined:
			var p struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Use    string `json:"use"`
				Shared bool   `json:"shared"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if p.ID == "" || p.Name == "" {
				continue
			}
			w := Workspace{ID: p.ID, Name: p.Name, Use: p.Use, Shared: p.Shared}
			if i, ok := wsSeen[p.ID]; ok {
				v.Workspaces[i] = w
			} else {
				wsSeen[p.ID] = len(v.Workspaces)
				v.Workspaces = append(v.Workspaces, w)
			}

		case FactProjectCreated, FactProjectJoined:
			var p struct {
				ID     string         `json:"id"`
				Ws     string         `json:"ws"`
				Name   string         `json:"name"`
				Owner  string         `json:"owner"`
				Shared bool           `json:"shared"`
				BRD    *ProjectBRDRef `json:"brd"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if p.ID == "" || p.Name == "" {
				continue
			}
			pj := ProjectRow{ID: p.ID, Ws: p.Ws, Name: p.Name, Owner: p.Owner, Shared: p.Shared, BRD: p.BRD}
			if i, ok := pjSeen[p.ID]; ok {
				// 后到的 BRD 摘要合并进已有项目（先建项目、再写 BRD 是正常顺序）。
				if pj.BRD == nil {
					pj.BRD = v.Projects[i].BRD
				}
				v.Projects[i] = pj
			} else {
				pjSeen[p.ID] = len(v.Projects)
				v.Projects = append(v.Projects, pj)
			}

		case FactBRDSaved:
			// BRD 属于项目：保存时把摘要挂到项目上，侧栏才能显示 v1.2 / 待建。
			var p struct {
				Proj string `json:"proj"`
				V    string `json:"v"`
				At   string `json:"at"`
				St   string `json:"st"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			pid := p.Proj
			if pid == "" {
				pid = DefaultProject
			}
			if i, ok := pjSeen[pid]; ok {
				v.Projects[i].BRD = &ProjectBRDRef{V: p.V, File: v.Projects[i].Name + " · 需求说明书", At: p.At}
			}
		}
	}
	return v, nil
}

// DefaultProject 是种子项目的 id：老账本里没有 proj 字段的单元/BRD 都归它。
const DefaultProject = "p1"

// DefaultCatalog 是「加入已有」的候选。
//
// 演示数据：候选是别人那边的东西，不在这本账里。真接多租户时
// 这张表由服务端按权限下发，形状不变。
func DefaultCatalog() Catalog {
	return Catalog{
		Projects: []CatalogProject{
			{ID: "c1", Name: "门禁与访客改造", Ws: "星河湾", Owner: "李月", Members: 4, Note: "智能通行 / 访客预约 / 与收费联动"},
			{ID: "c2", Name: "客服质检二期", Ws: "悦府物业", Owner: "梦杰", Members: 3, Note: "质检规则扩展 + 复核工作台"},
			{ID: "c3", Name: "公域投放归因看板", Ws: "星河湾", Owner: "斯媛", Members: 2, Note: "投放 → 私域 → 成交归因"},
		},
		Spaces: []CatalogSpace{
			{ID: "s1", Name: "星河湾", Projects: 3, Note: "门禁、巡更、能耗三个项目"},
			{ID: "s2", Name: "集团共享", Projects: 7, Note: "集团级公共项目与模板"},
		},
	}
}
