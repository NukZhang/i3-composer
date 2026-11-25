package bff

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/store"
)

// 本文件是 v2.7 的「工作空间 → 项目」这一层。
//
// 之前项目是写死在前端的一个常量：所有交付单元挂在它下面，开不了第二个。
// 现在项目在账本里：新建、加入已有、不归入任何工作空间，都是事实。
//
// 权限：建项目 / 建工作空间是 PA 的活（ActPbc）；写 BRD 是 BA 的活（ActBrd）。

const projectsLog = "projects"

func (s *Server) loadProjects(ctx context.Context) (*store.ProjectsView, error) {
	facts, err := s.Log.Load(ctx, projectsLog)
	if err != nil {
		return nil, err
	}
	return store.ProjectProjects(facts)
}

// projectExists 判定一个项目在不在账本里。
func (s *Server) projectExists(ctx context.Context, id string) (bool, error) {
	pv, err := s.loadProjects(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range pv.Projects {
		if p.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// projects 列出工作空间、项目与「可加入」的候选。
func (s *Server) projects(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	// 每个项目下有几个交付单元：侧栏和项目页都要用。
	count := map[string]int{}
	for _, u := range uv.Units {
		count[u.Proj]++
	}
	// BRD 摘要：版本线在 units 日志里，项目日志只存「挂了哪份 BRD」。
	// 这里按项目重放一次，让侧栏的「v1.2 / 待建」跟着真实版本走。
	facts, err := s.Log.Load(r.Context(), unitsLog)
	if err != nil {
		return err
	}
	for i := range pv.Projects {
		brd, err := store.ProjectBRD(facts, pv.Projects[i].ID)
		if err != nil {
			return err
		}
		if len(brd.Vers) > 0 {
			pv.Projects[i].BRD = &store.ProjectBRDRef{
				V: brd.V, File: pv.Projects[i].Name + " · 需求说明书", At: brd.At,
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seq":        pv.Seq,
		"workspaces": pv.Workspaces,
		"projects":   pv.Projects,
		"catalog":    pv.Catalog,
		"unitCount":  count,
	})
	return nil
}

// newProjectID 给新项目编号：p + 时间戳后 4 位（与原型一致的做法）。
// 撞了就在后面加一位，直到不重。
func (s *Server) newProjectID(ctx context.Context) (string, error) {
	pv, err := s.loadProjects(ctx)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, p := range pv.Projects {
		used[p.ID] = true
	}
	base := strings.ToLower(newID("p"))
	return uniqueID(base, used), nil
}

// uniqueID 在 base 已占用时依次试 base0、base1……，直到不重。
// 项目与工作空间都走这里：随机碰撞概率低，但账本是长期存在的，
// 撞号会静默把两条记录合成一条，事后很难看出发生过什么。
func uniqueID(base string, used map[string]bool) string {
	id := base
	for i := 0; used[id]; i++ {
		id = fmt.Sprintf("%s%d", base, i)
	}
	return id
}

// createProject 新建一个项目。start=brd 时顺带建 0.1 的 BRD 草稿。
func (s *Server) createProject(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActPbc); err != nil {
		return err
	}
	var in struct {
		Name  string `json:"name"`
		Ws    string `json:"ws"`
		Start string `json:"start"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 40 {
		return fmt.Errorf("%w：项目名称必填且不超过 40 字", errBadRequest)
	}
	if in.Ws != "" {
		pv, err := s.loadProjects(r.Context())
		if err != nil {
			return err
		}
		ok := false
		for _, w := range pv.Workspaces {
			if w.ID == in.Ws {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%w：工作空间 %s 不存在", errBadRequest, in.Ws)
		}
	}
	id, err := s.newProjectID(r.Context())
	if err != nil {
		return err
	}
	if err := s.Log.Ensure(r.Context(), projectsLog); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), projectsLog, store.FactProjectCreated, c.Role.String(), map[string]any{
		"id": id, "ws": in.Ws, "name": in.Name, "owner": c.Role.String(),
	}); err != nil {
		return err
	}
	// 先写 BRD（0.1 模板），再把项目摘要挂上去：侧栏要显示「待建 / v0.1」。
	if in.Start == "brd" {
		if _, err := s.startBRDFor(r.Context(), id, in.Name, c.Role.String()); err != nil {
			return err
		}
	}
	s.publish("projects", "project.created", map[string]any{"id": id, "name": in.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": in.Name})
	return nil
}

// startBRD 给已有项目建 0.1 的 BRD 草稿（正文是空白模板）。
func (s *Server) startBRD(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBrd); err != nil {
		return err
	}
	id := r.PathValue("id")
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	var proj *store.ProjectRow
	for i := range pv.Projects {
		if pv.Projects[i].ID == id {
			proj = &pv.Projects[i]
		}
	}
	if proj == nil {
		return fmt.Errorf("%w：项目 %s 不存在", errNotFound, id)
	}
	v, err := s.startBRDFor(r.Context(), id, proj.Name, c.Role.String())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]any{"v": v, "proj": id})
	return nil
}

// startBRDFor 写一版 0.1 的空白模板。已有 BRD 就不重复建。
func (s *Server) startBRDFor(ctx context.Context, proj, name, by string) (string, error) {
	facts, err := s.Log.Load(ctx, unitsLog)
	if err != nil {
		return "", err
	}
	cur, err := store.ProjectBRD(facts, proj)
	if err != nil {
		return "", err
	}
	if len(cur.Vers) > 0 {
		return cur.V, nil
	}
	text := brdTemplate(name)
	if err := s.Log.Ensure(ctx, unitsLog); err != nil {
		return "", err
	}
	if _, err := s.Log.Append(ctx, unitsLog, store.FactBRDSaved, by, map[string]any{
		"proj": proj, "v": "v0.1", "at": nowStamp(), "by": by, "st": "draft",
		"note": "新建 BRD：空白模板", "text": text,
	}); err != nil {
		return "", err
	}
	s.publish("units", "brd.saved", map[string]any{"proj": proj, "v": "v0.1"})
	return "v0.1", nil
}

// brdTemplate 是新项目 BRD 的骨架。
//
// 它不是悦府那份的内容：新项目不该继承别人的业务。
func brdTemplate(name string) string {
	return "# " + name + " · 需求说明书\n\n" +
		"## 1. 背景\n（谁、在什么场景、现在怎么解决、痛在哪）\n\n" +
		"## 2. 目标\n（做完之后哪个数会变；不做的话哪个数不会变）\n\n" +
		"## 3. 范围\n### 3.1 做\n### 3.2 不做\n\n" +
		"## 4. 关键规则\n（贴业务方原话；拿不准的写「待确认」）\n\n" +
		"## 5. 验收口径\n（怎么算做完了）\n"
}

// createWorkspace 新建工作空间。
func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActPbc); err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
		Use  string `json:"use"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 40 {
		return fmt.Errorf("%w：工作空间名称必填且不超过 40 字", errBadRequest)
	}
	if in.Use != "" && in.Use != "org" && in.Use != "team" {
		return fmt.Errorf("%w：用途只能是 org（按客户）或 team（按团队）", errBadRequest)
	}
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, w := range pv.Workspaces {
		used[w.ID] = true
	}
	// 随机 id 也要查重：账本是长期的，撞号会让两个工作空间共用一条记录。
	id := uniqueID(strings.ToLower(newID("w")), used)
	if err := s.Log.Ensure(r.Context(), projectsLog); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), projectsLog, store.FactWorkspaceCreated, c.Role.String(), map[string]any{
		"id": id, "name": in.Name, "use": in.Use,
	}); err != nil {
		return err
	}
	s.publish("projects", "workspace.created", map[string]any{"id": id, "name": in.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": in.Name})
	return nil
}

// joinProject 把「已有项目」加进自己的侧栏。
//
// 加入不等于复制：项目还在原处、还是原来的人在管，只是出现在你的侧栏。
// 这里落一条 project.joined（shared=true），原处那条 project.created 不动。
func (s *Server) joinProject(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActPbc); err != nil {
		return err
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	var cand *store.CatalogProject
	for i := range pv.Catalog.Projects {
		if pv.Catalog.Projects[i].ID == in.ID {
			cand = &pv.Catalog.Projects[i]
		}
	}
	if cand == nil {
		return fmt.Errorf("%w：候选项 %s 不存在", errBadRequest, in.ID)
	}
	for _, p := range pv.Projects {
		if p.ID == in.ID {
			return fmt.Errorf("%w：已经在侧栏里了", errBadRequest)
		}
	}
	// 原归属空间已经在账本里就挂上去，没有就留空（不替别人建空间）。
	wsID := ""
	for _, w := range pv.Workspaces {
		if w.Name == cand.Ws {
			wsID = w.ID
		}
	}
	if err := s.Log.Ensure(r.Context(), projectsLog); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), projectsLog, store.FactProjectJoined, c.Role.String(), map[string]any{
		"id": cand.ID, "ws": wsID, "name": cand.Name, "owner": cand.Owner, "shared": true,
		"brd": map[string]any{"v": "v1.0", "file": cand.Name + " · 需求说明书.docx", "size": "1.6 MB", "at": "11-19 10:00"},
	}); err != nil {
		return err
	}
	s.publish("projects", "project.joined", map[string]any{"id": cand.ID, "name": cand.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"id": cand.ID, "name": cand.Name, "shared": true})
	return nil
}

// joinWorkspace 把一个已有工作空间加进侧栏。
func (s *Server) joinWorkspace(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActPbc); err != nil {
		return err
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	pv, err := s.loadProjects(r.Context())
	if err != nil {
		return err
	}
	var cand *store.CatalogSpace
	for i := range pv.Catalog.Spaces {
		if pv.Catalog.Spaces[i].ID == in.ID {
			cand = &pv.Catalog.Spaces[i]
		}
	}
	if cand == nil {
		return fmt.Errorf("%w：候选项 %s 不存在", errBadRequest, in.ID)
	}
	for _, w := range pv.Workspaces {
		if w.Name == cand.Name {
			return fmt.Errorf("%w：这个工作空间已经加过了", errBadRequest)
		}
	}
	if err := s.Log.Ensure(r.Context(), projectsLog); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), projectsLog, store.FactWorkspaceJoined, c.Role.String(), map[string]any{
		"id": cand.ID, "name": cand.Name, "shared": true,
	}); err != nil {
		return err
	}
	s.publish("projects", "workspace.joined", map[string]any{"id": cand.ID, "name": cand.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"id": cand.ID, "name": cand.Name, "shared": true})
	return nil
}

// nowStamp 是「月-日 时:分」，账本里的人读时间。
func nowStamp() string {
	return time.Now().Format("01-02 15:04")
}

// newID 生成短 id：前缀 + 4 位十六进制随机数。撞了由调用方重试。
func newID(prefix string) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
