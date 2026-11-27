package bff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	path "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangkun/i3-composer/internal/agentexec"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

// Server 是 L4 的 HTTP 面。
type Server struct {
	Log  *store.Store
	Dapr *dapr.Client
	Sess *Sessions
	Comp string // workflow 组件路径段；Dapr 目前不校验它，但它是路由契约
	// WfName 是 workflow 的名字。名字不同 = Dapr 里不同的 actor 类型，
	// 多套流程可以并存而不抢工作项。
	WfName string
	// Exec 是 Agent 执行器：试跑与对话验收都靠它真跑一遍编排，
	// 不是把定义里存的样例端出来。
	Exec *agentexec.Exec
	// Pipe 是执行侧：发布批次时用它真部署、回滚时用它收进程。
	// 为 nil 时发布编排会明确报错，而不是假装发成功。
	Pipe *pipeline.Pipeline
	// LocalPort 是本进程实际监听的端口（0 = 未知）。
	// sidecar 把 pubsub 事件投到它配置的 app-port；两者不一致时事件会在别处落地，
	// 验收里只看到「收到 0 条」，看不出原因。见 CheckPubsubWiring。
	LocalPort int
	WebDir    string
	// Pubsub 是启动时算出的投递地址校验结果，/api/health 原样带出。
	Pubsub map[string]any

	mu                sync.Mutex
	subs              map[chan string]struct{} // SSE 订阅者（进程内即可：断线重连是常态）
	materialPreviewMu sync.Mutex
	releaseMu         sync.Mutex
	unitRegistryMu    sync.Mutex
}

func NewServer(log *store.Store, d *dapr.Client, sess *Sessions, comp string) *Server {
	return &Server{Log: log, Dapr: d, Sess: sess, Comp: comp,
		Exec: &agentexec.Exec{},
		subs: map[chan string]struct{}{}}
}

// wfName 是本实例使用的 workflow 名；没配就用默认名。
func (s *Server) wfName() string {
	if s.WfName != "" {
		return s.WfName
	}
	return wf.NameDelivery
}

// instanceID 是 Dapr 侧的 workflow 实例号。
//
// 它带账本前缀：同一套 Dapr 上跑多套账本时，实例号也必须隔开，
// 否则两套账本的工作项会抢同一个 workflow 实例。
// Dapr 只允许字母数字与下划线，所以前缀里的其它字符要换成下划线。
func (s *Server) instanceID(itemID string) string {
	p := s.Log.Prefix()
	if p == "" {
		return itemID
	}
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String() + itemID
}

// Handler 装路由。业务接口全部以 /api 开头；其余路径交给静态页（web/）。
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("GET /api/state", s.wrap(s.state))
	m.HandleFunc("GET /api/overview", s.wrap(s.overview))
	m.HandleFunc("GET /api/inbox", s.wrap(s.inbox))
	m.HandleFunc("GET /api/notifications", s.wrap(s.notifications))
	m.HandleFunc("GET /api/tasks", s.wrap(s.tasks))
	m.HandleFunc("POST /api/tasks", s.wrap(s.createTask))
	m.HandleFunc("GET /api/items/{id}", s.wrap(s.getItem))
	m.HandleFunc("POST /api/items/{id}/run", s.wrap(s.runItem))
	// 准入被拒后补输入：补的是验收口径 / 数据范围 / 责任人，发起时重判。
	m.HandleFunc("POST /api/items/{id}/inputs", s.wrap(s.admitInputs))
	m.HandleFunc("POST /api/role", s.wrap(s.setRole))
	m.HandleFunc("GET /api/timeline/stream", s.stream)

	// v2.3：交付单元 / BRD / Agent / 服务验收 / 批注
	m.HandleFunc("GET /api/units", s.wrap(s.units))
	m.HandleFunc("POST /api/units", s.wrap(s.createUnit))
	m.HandleFunc("DELETE /api/units/{id}", s.wrap(s.removeUnit))
	m.HandleFunc("POST /api/units/{id}/svcverify", s.wrap(s.svcVerify))
	m.HandleFunc("GET /api/brd", s.wrap(s.brd))
	m.HandleFunc("POST /api/brd", s.wrap(s.saveBRD))
	// v2.7：工作空间 → 项目。项目是承接容器，新建 / 加入已有都进账本。
	m.HandleFunc("GET /api/projects", s.wrap(s.projects))
	m.HandleFunc("POST /api/projects", s.wrap(s.createProject))
	m.HandleFunc("POST /api/projects/join", s.wrap(s.joinProject))
	m.HandleFunc("POST /api/projects/{id}/brd", s.wrap(s.startBRD))
	m.HandleFunc("POST /api/workspaces", s.wrap(s.createWorkspace))
	m.HandleFunc("POST /api/workspaces/join", s.wrap(s.joinWorkspace))
	// v3.2：单元把 tag 打进「发布编排」池；发布是项目级动作，批次只能整批回滚。
	m.HandleFunc("POST /api/units/{id}/tag", s.wrap(s.tagUnit))
	m.HandleFunc("POST /api/units/{id}/material-preview", s.wrap(s.startMaterialPreview))
	m.HandleFunc("GET /pbc/{id}/{env}/{rest...}", s.wrap(s.materialRuntime))
	m.HandleFunc("POST /pbc/{id}/{env}/api/inbounds", s.wrap(s.materialRuntime))
	// 只读回放：按 tag.created 的时点重算当时的投影，回答「凭什么能发」。
	m.HandleFunc("GET /api/units/{id}/replay", s.wrap(s.replay))
	m.HandleFunc("GET /api/projects/{id}/orch", s.wrap(s.orch))
	m.HandleFunc("GET /api/projects/{id}/releases/{batchID}/units/{unitID}/health", s.wrap(s.releaseUnitHealth))
	m.HandleFunc("POST /api/projects/{id}/orch/release", s.wrap(s.release))
	m.HandleFunc("POST /api/projects/{id}/orch/rollback", s.wrap(s.rollback))
	m.HandleFunc("GET /api/agents", s.wrap(s.agents))
	m.HandleFunc("GET /api/agents/{id}", s.wrap(s.agent))
	m.HandleFunc("POST /api/agents/{id}/eval", s.wrap(s.agentEval))
	m.HandleFunc("POST /api/agents/{id}/baseline", s.wrap(s.agentBaseline))
	m.HandleFunc("POST /api/agents/{id}/publish", s.wrap(s.agentPublish))
	m.HandleFunc("POST /api/agents/{id}/rollout", s.wrap(s.agentRollout))
	m.HandleFunc("POST /api/agents/{id}/rollback", s.wrap(s.agentRollback))
	m.HandleFunc("POST /api/annotations", s.wrap(s.addAnnotation))
	m.HandleFunc("POST /api/artifacts", s.wrap(s.saveDraft))
	m.HandleFunc("GET /api/facts", s.wrap(s.facts))
	// v2.6：Agent 编排、构建、试跑、沉淀用例；知识库问题单
	m.HandleFunc("PUT /api/agents/{id}/flow", s.wrap(s.agentFlow))
	m.HandleFunc("POST /api/agents/{id}/build", s.wrap(s.agentBuild))
	m.HandleFunc("POST /api/agents/{id}/trial", s.wrap(s.agentTrial))
	m.HandleFunc("POST /api/agents/{id}/cases", s.wrap(s.agentCase))
	m.HandleFunc("POST /api/agents/{id}/kb/{idx}/dispatch", s.wrap(s.kbDispatch))
	// v2.5：真人验收台
	m.HandleFunc("POST /api/checks/{id}/decide", s.wrap(s.checkDecide))
	m.HandleFunc("POST /api/units/{id}/chat", s.wrap(s.verifyChat))
	// v2.6：pubsub 收发。/dapr/subscribe 是 sidecar 问订阅的地方；
	// /api/events/{topic} 是事件投递进来的地方。
	m.HandleFunc("GET /dapr/subscribe", s.daprSubscribe)
	m.HandleFunc("POST /api/events/{topic}", s.wrap(s.receiveEvent))
	m.HandleFunc("GET /api/events", s.wrap(s.receivedEvents))
	// ServeMux 里 /api/... 比 / 更具体，优先匹配，静态兜底不会抢接口。
	webDir := s.WebDir
	if webDir == "" {
		webDir = "web/dist"
	}
	m.Handle("/", spa(http.Dir(webDir)))
	return m
}

// 请求上下文。
type reqCtx struct {
	Role   Role
	PBC    string
	ItemID string
}

type handler func(http.ResponseWriter, *http.Request, reqCtx) error

// wrap 统一：取角色、取 pbc、错误映射。业务 handler 只管业务。
func (s *Server) wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := s.Sess.Get(r)
		pbc := strings.TrimSpace(r.URL.Query().Get("pbc"))
		if pbc == "" {
			pbc = "fee" // 单 PBC 阶段的默认；多 PBC 时由前端显式带
		}
		ctx := reqCtx{Role: role, PBC: pbc, ItemID: r.PathValue("id")}
		if err := h(w, r, ctx); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrForbidden) {
				code = http.StatusForbidden
			} else if errors.Is(err, errBadRequest) {
				code = http.StatusBadRequest
			} else if errors.Is(err, errNotFound) {
				code = http.StatusNotFound
			}
			slog.Warn("bff", "path", r.URL.Path, "role", role, "err", err)
			writeJSON(w, code, map[string]any{"error": err.Error()})
		}
	}
}

var (
	errBadRequest = errors.New("请求不合法")
	errNotFound   = errors.New("不存在")
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// spa 托管 Vite 产物，并对「前端路由」回落到 index.html。
//
// 为什么需要回落：页面路由（/plan、/verify 等）在服务端没有对应文件，
// 直接 404 会让刷新页面变成白屏。判据是「路径里有扩展名」——
// 有扩展名的是静态资源，没有的是前端路由。
//
// 例外是 /api/ 前缀：那是接口，不是前端路由。拼错的路由、用错方法的接口
// 必须如实回 404 / 405，而不是回 200 + 首页——否则客户端 if (resp.ok) 会进成功
// 分支再解析 HTML 失败，错误信息离真正的「接口不存在」很远（宪法 II：拒绝要说得清）。
func spa(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" || strings.Contains(path.Ext(p), ".") {
			files.ServeHTTP(w, r)
			return
		}
		// 接口路由到这里说明没匹配上：是「不存在」，不是前端页面。
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "不存在：接口 " + p + " 没有对应路由"})
			return
		}
		idx, err := root.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer idx.Close()
		http.ServeContent(w, r, "index.html", time.Time{}, idx)
	})
}

// ---------- 视图 ----------

// Task 是 backlog 行在前端的样子。字段名沿用原型，前端布局不用改。
type Task struct {
	ID        string       `json:"id"`
	Title     string       `json:"t"`
	PT        int          `json:"pt"`
	St        string       `json:"st"`
	Owner     string       `json:"owner"`
	Kind      string       `json:"kind"`
	Parent    string       `json:"parent,omitempty"`
	PBC       string       `json:"pbc"`
	Type      string       `json:"type"`
	Mode      string       `json:"mode"`
	Env       string       `json:"env"`
	Run       string       `json:"run"`
	Step      int          `json:"step"`
	Steps     []store.Step `json:"steps,omitempty"`
	Version   string       `json:"version,omitempty"`
	Tag       string       `json:"tag,omitempty"`
	URL       string       `json:"url,omitempty"`
	By        string       `json:"by,omitempty"`
	Blocked   string       `json:"blocked,omitempty"`
	BlockedBy string       `json:"blockedBy,omitempty"`
	At        string       `json:"at,omitempty"`
	// Admit 是入口准入的最近一次结论：页面据此挂 waiver 黄条或拒单三行。
	Admit  *store.AdmitInfo `json:"admit,omitempty"`
	Repair *store.RepairRef `json:"repair,omitempty"`
}

func toTask(it *store.Item) Task {
	return Task{
		ID: it.ID, Title: it.Title, PT: it.PT, St: it.St, Owner: it.Owner, Kind: it.Kind,
		Parent: it.Parent,
		PBC:    it.PBC, Type: it.Type, Mode: it.Mode, Env: it.Env, Run: it.Run, Step: it.Step,
		Steps: it.Steps, Version: it.Version, Tag: it.Tag, URL: it.URL, By: it.By,
		Blocked: it.Blocked, BlockedBy: it.BlockedBy, At: it.At, Admit: it.Admit, Repair: it.Repair,
	}
}

func (s *Server) load(r *http.Request, pbc string) (*store.View, error) {
	facts, err := s.Log.Load(r.Context(), pbc)
	if err != nil {
		return nil, err
	}
	return store.Project(pbc, facts)
}

// state 与 Agent 相关的读取在 v23.go：同一条 /api/state 路由，
// 但 v2.3 起还要把交付单元构成与门禁结论一并带上。

// NotifyChange 由事实日志在每次成功落链后回调，把「某个 PBC 变了」推给 SSE。
// 它不携带账本内容：订阅者收到后重新 GET 全量投影。
func (s *Server) NotifyChange(pbc string) {
	s.publish(pbc, "fact.appended", nil)
}

// overview 返回当前单元的工作项、会话权限与交付进度。
func (s *Server) overview(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	all := make([]Task, 0, len(v.Items))
	for _, it := range v.Items {
		all = append(all, toTask(it))
	}
	head := ""
	if len(v.Commits) > 0 {
		head = v.Commits[0].No
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role":  c.Role,
		"name":  roleNames[c.Role],
		"acts":  roleActs[c.Role],
		"pbc":   c.PBC,
		"items": all,
		"progress": map[string]any{
			"pbc":     c.PBC,
			"head":    head,
			"tags":    v.Tags,
			"commits": len(v.Commits),
		},
	})
	return nil
}

// tasks 为旧的只读客户端保留 tab=review，以便查询历史等待记录。
func (s *Server) tasks(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	tab := r.URL.Query().Get("tab")
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	out := make([]Task, 0, len(v.Items))
	for _, it := range v.Items {
		if match(tab, c.Role, it) {
			out = append(out, toTask(it))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tab": tab, "items": out})
	return nil
}

func match(tab string, role Role, it *store.Item) bool {
	switch tab {
	case "review":
		return it.Run == "waiting"
	case "mine":
		return it.By == string(role)
	default: // todo
		return it.Owner == string(role) && it.St != "done" && it.Run != "waiting" && it.Run != "published"
	}
}

// createTask 建一条 backlog。人或飞书卡片都走这里——同一张表。
func (s *Server) createTask(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBacklog); err != nil {
		return err
	}
	var in struct {
		Title    string   `json:"title"`
		Type     string   `json:"type"`
		Env      string   `json:"env"`
		Mode     string   `json:"mode"`
		Accept   []string `json:"accept"`
		Data     []string `json:"data"`
		AcceptBy string   `json:"acceptBy"`
		ComplyBy string   `json:"complyBy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len([]rune(in.Title)) > 80 {
		return fmt.Errorf("%w：标题必填且不超过 80 字", errBadRequest)
	}
	if in.Type == "" {
		in.Type = "feature"
	}
	if in.Env == "" {
		in.Env = "test"
	}
	if in.Mode == "" {
		in.Mode = "fast"
	}
	// 准入输入随建单一起落账。它们此时可以空——准入是「发起交付」那道门的事，
	// 不是建单的门：需求池允许先记下一句要做什么，再回头补口径。
	// 但写了就要看得见，所以原样进事实，不在这里悄悄丢掉。

	// 编号在全项目范围内取：需求池是各单元工作项合起来看的，
	// 各单元各自从 US-01 开始会出现两个 US-01，谁也说不清指哪一条。
	s.unitRegistryMu.Lock()
	defer s.unitRegistryMu.Unlock()
	id, err := s.nextGlobalItemID(r, c.PBC)
	if err != nil {
		return err
	}
	it := store.Item{
		ID: id, Title: in.Title, PT: 3, St: "todo", Owner: "—", Kind: "Story",
		PBC:  c.PBC,
		Type: in.Type, Mode: in.Mode, Env: in.Env, Run: "idea",
		Accept: in.Accept, Data: in.Data,
		AcceptBy: strings.TrimSpace(in.AcceptBy), ComplyBy: strings.TrimSpace(in.ComplyBy),
		By: string(c.Role), At: time.Now().Format("01-02 15:04"),
	}
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemCreated, string(c.Role), it); err != nil {
		return err
	}
	s.publish(c.PBC, "item.created", map[string]any{"id": id, "title": in.Title})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	return nil
}

func nextItemID(items map[string]*store.Item) (string, error) {
	n := 0
	for _, it := range items {
		if it.Kind != "Story" {
			continue
		}
		var k int
		if _, err := fmt.Sscanf(it.ID, "US-%d", &k); err == nil && k > n {
			n = k
		}
	}
	if n >= 99 {
		return "", fmt.Errorf("%w：US 编号已到上限 99", errBadRequest)
	}
	return fmt.Sprintf("US-%02d", n+1), nil
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	it, ok := v.Items[c.ItemID]
	if !ok {
		return fmt.Errorf("%w：%s", errNotFound, c.ItemID)
	}
	// 事实流：把该 item 相关的事实按时间给出。
	// payload 只给摘要（标量字段），不给正文——制品的代码、文档正文不进这个出口。
	var facts []map[string]any
	fs, err := s.Log.Load(r.Context(), c.PBC)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if strings.Contains(string(f.Payload), `"id":"`+c.ItemID+`"`) ||
			strings.Contains(string(f.Payload), `"itemId":"`+c.ItemID+`"`) {
			facts = append(facts, map[string]any{
				"seq": f.Seq, "at": f.At, "type": f.Type, "actor": f.Actor,
				"payload": factScalars(f.Payload),
			})
		}
	}
	out := map[string]any{"item": toTask(it), "facts": facts}
	if it.Repair != nil {
		out["repairInputs"] = map[string]any{
			"accept": it.Accept, "data": it.Data, "acceptBy": it.AcceptBy, "complyBy": it.ComplyBy,
			"reasons": domain.Admit(s.admitInput(v, it)).Reasons,
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// factScalars 抽事实 payload 里的标量字段。
//
// 事实的正文有时是整份制品（代码树、文档正文），原样返回既臃肿又不该进这个出口；
// 但「测试几条过、digest 是什么、部署在哪」这些结论必须能就地核对，
// 否则账本只是「发生过什么」的目录，看不出「到底发生了什么」。
func factScalars(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // 数字原样输出，不被浮点化
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return out
	}
	for k, v := range m {
		switch tv := v.(type) {
		case string, bool, json.Number, nil:
			out[k] = tv
		case []any:
			// 只留标量数组（失败用例名之类），且截断，避免把大数组塞进列表。
			items := make([]any, 0, len(tv))
			scalars := true
			for _, e := range tv {
				switch et := e.(type) {
				case string, bool, json.Number:
					items = append(items, et)
				default:
					scalars = false
				}
			}
			if scalars && len(items) > 0 {
				if len(items) > 20 {
					items = items[:20]
				}
				out[k] = items
			}
		}
	}
	return out
}

// runItem 发起一条 backlog 的交付。
//
// 这里最关键的一行是 InstanceID = itemID：Dapr 对同一实例重复 start 返回 409，
// 这就是「同一 backlog 不许并发跑」的锁，不需要自己实现。
func (s *Server) runItem(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBuild); err != nil {
		return err
	}
	s.unitRegistryMu.Lock()
	defer s.unitRegistryMu.Unlock()
	units, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	if _, removed := units.Removed[c.PBC]; removed {
		return fmt.Errorf("%w：单元 %s 已作废", errNotFound, c.PBC)
	}
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	it, ok := v.Items[c.ItemID]
	if !ok {
		return fmt.Errorf("%w：%s", errNotFound, c.ItemID)
	}
	if it.Run == "running" || it.Run == "waiting" {
		return fmt.Errorf("%w：%s 已在执行中（run=%s）", errBadRequest, c.ItemID, it.Run)
	}
	// 已发布的不许再跑：原型只给 blocked 提供「重新发起」，
	// 已上线的工作项要变更就得开新的工作项，否则账本里会出现
	// 「同一项发布了两个 tag」这种无法回溯的歧义。
	if it.Run == "published" {
		return fmt.Errorf("%w：%s 已发布（%s），如需变更请新建工作项", errBadRequest, c.ItemID, it.Tag)
	}
	if it.Kind != "Story" || len(it.Steps) == 0 {
		return fmt.Errorf("%w：%s 不是可执行的工作项", errBadRequest, c.ItemID)
	}
	if v.RepairCheckPassed(it) {
		return fmt.Errorf("%w：验收项 %s 已通过，如需继续修复请重新登记失败", errBadRequest, it.Repair.Check)
	}

	// 入口准入：硬项不过就停在这里，不落 item.run.started、不调 sidecar。
	// 与宪法 V「越权不留事实」同一条道理——被拒的发起不该留下半条链，
	// 否则账本里会出现一条「跑过」却没有任何步骤的事实指向不存在的执行。
	adm := domain.Admit(s.admitInput(v, it))
	if !adm.Pass {
		reasons := make([]string, 0, len(adm.Reasons))
		for _, r := range adm.Reasons {
			reasons = append(reasons, fmt.Sprintf("%s：%s；找 %s；%s", r.Label, r.Missing, r.Ask, r.Fix))
		}
		// 拒绝也要留痕：账本记「谁在什么时候被哪几项挡住」，页面才解释得清。
		if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemAdmitRejected, c.Role.String(),
			map[string]any{"id": it.ID, "reasons": reasons, "by": c.Role.String()}); err != nil {
			return err
		}
		s.publish(c.PBC, "item.admit.rejected", map[string]any{"id": it.ID})
		return fmt.Errorf("%w：%s 入口准入没过\n%s", errBadRequest, it.ID, strings.Join(reasons, "\n"))
	}
	// 软项不过放行，但把「哪项没补」落成事实，页面顶部挂黄条——
	// 别把自己卡死，也别假装五项都判过了。
	//
	// 落账放在实例真的起来之后（见下）：409 锁冲突或 sidecar 启动失败时，
	// 这次发起并没有发生，账本里就不该留一条「被准入放行」。

	// Ledger 跟着工作项走：工作项落到哪个进程执行是 Dapr 决定的，
	// 账本前缀必须由发起方带过去，不能由执行方自己猜。
	in := wf.Input{ItemID: it.ID, PBC: it.PBC, Title: it.Title, Type: it.Type, Env: it.Env, Mode: it.Mode,
		Ledger: s.Log.Prefix(), Unit: &wf.UnitInfo{ID: it.PBC, Name: it.PBC, Kind: string(domain.KindPBC)}}
	for _, unit := range units.Units {
		if unit.ID == in.PBC {
			in.Unit = &wf.UnitInfo{ID: unit.ID, Name: unit.Name, Kind: unit.Kind, AgentID: unit.AgentID, Profile: unit.Profile}
			break
		}
	}
	// 实例号 = 账本前缀 + 单元 + 工作项。单元必须进来：不同单元下
	// 各有一份 US-01 是常态，只带工作项号会让两份工作项抢同一个实例。
	inst := s.instanceID(c.PBC + "_" + it.ID)
	if it.Repair != nil {
		in.RepairStart = &wf.RepairStart{
			OpID: fmt.Sprintf("%s/repair/%d", inst, v.Seq+1),
			By:   c.Role.String(), Waivers: adm.Waivers,
		}
	}
	err = s.Dapr.StartWorkflow(r.Context(), s.Comp, s.wfName(), inst, in)
	if errors.Is(err, dapr.ErrConflict) {
		return fmt.Errorf("%w：%s 已有一个未结束的实例", errBadRequest, c.ItemID)
	}
	if err != nil {
		return err
	}
	if in.RepairStart != nil {
		started, recordErr := wf.RecordRepairStart(r.Context(), s.Log, v, in)
		if recordErr != nil && !errors.Is(recordErr, store.ErrSnapshotChanged) {
			slog.Warn("修复启动记录交由工作流补记", "item", it.ID, "err", recordErr)
		}
		if started {
			s.publish(c.PBC, "item.admitted", map[string]any{"id": it.ID, "waivers": len(adm.Waivers)})
			s.publish(c.PBC, "item.run.started", map[string]any{"id": it.ID})
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": it.ID, "instance": inst})
		return nil
	}
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemAdmitted, c.Role.String(),
		map[string]any{"id": it.ID, "waivers": adm.Waivers, "by": c.Role.String()}); err != nil {
		return err
	}
	s.publish(c.PBC, "item.admitted", map[string]any{"id": it.ID, "waivers": len(adm.Waivers)})
	// 先记一条起始事实，让投影立刻显示 running，不必等 activity 回来。
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemRunStarted, c.Role.String(),
		map[string]any{"id": it.ID}); err != nil {
		return err
	}
	s.publish(c.PBC, "item.run.started", map[string]any{"id": it.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": it.ID, "instance": inst})
	return nil
}

// setRole 切换会话角色。角色是会话级的，不写进账本。
func (s *Server) setRole(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	var in struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	nr := Role(in.Role)
	if !nr.Valid() {
		return fmt.Errorf("%w：未知角色 %q", errBadRequest, in.Role)
	}
	s.Sess.Issue(w, nr)
	writeJSON(w, http.StatusOK, map[string]any{"role": nr, "name": roleNames[nr], "acts": roleActs[nr]})
	return nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)}
	if s.Pubsub != nil {
		body["pubsub"] = s.Pubsub
	}
	writeJSON(w, http.StatusOK, body)
}

// CheckPubsubWiring 对比 sidecar 的 app-port 与本进程端口。
//
// 为什么要显式校验：sidecar 由 docker-compose 固定 --app-port=8080，
// 事件也只会投到那个端口。把业务进程换个端口起（比如 8090）时，
// 工作流照样能跑（gRPC 由本进程外连）、但 pubsub 一条也收不到——
// 验收脚本只会报「收到 0 条」，看不出根因。把它算成一条结构化结论，
// 启动时打日志、/api/health 带出去，省一次排查。
//
// 读不到 sidecar 元数据时不判红（ok=true, checked=false）：
// 「没配 sidecar」和「配错了」是两件事，不能混成一条告警。
func (s *Server) CheckPubsubWiring(ctx context.Context) map[string]any {
	out := map[string]any{"localPort": s.LocalPort, "checked": false, "ok": true}
	if s.LocalPort == 0 {
		out["detail"] = "本进程监听端口未知（ADDR 里没有端口），跳过投递地址校验"
		return out
	}
	if s.Dapr == nil {
		out["detail"] = "未配置 Dapr 客户端，跳过投递地址校验"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ac, err := s.Dapr.AppConnection(ctx)
	if err != nil {
		out["detail"] = "读不到 sidecar 元数据，无法校验 pubsub 投递地址：" + err.Error()
		return out
	}
	out["checked"] = true
	out["sidecarAppPort"] = ac.Port
	out["sidecarAddress"] = ac.Address
	out["ok"] = ac.Port == s.LocalPort
	if ac.Port == s.LocalPort {
		out["detail"] = fmt.Sprintf("sidecar 的 app-port=%d，与本实例一致", ac.Port)
	} else {
		out["detail"] = fmt.Sprintf(
			"sidecar 的 app-port=%d，本实例监听 %d：pubsub 事件只会投到 %d，本实例一条也收不到。"+
				"让本实例监听 sidecar 的 app-port，或为本实例单独起一个 sidecar。",
			ac.Port, s.LocalPort, ac.Port)
	}
	return out
}

// PortOfAddr 从 ":8080" / "127.0.0.1:8080" 这类监听地址里取端口；取不到返回 0。
func PortOfAddr(addr string) int {
	_, value, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0
	}
	return int(port)
}

// ---------- SSE ----------

// stream 把事实变更推给时间线。它只推「有变化了」，不推账本内容——
// 前端收到通知后重新 GET，永远读到的是权威投影。
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()

	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", msg)
			fl.Flush()
		}
	}
}

// publish 通知所有 SSE 订阅者「某个 PBC 变了」。尽力而为：没人听就算了。
func (s *Server) publish(pbc, typ string, payload map[string]any) {
	b, _ := json.Marshal(map[string]any{"pbc": pbc, "type": typ, "data": payload})
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- string(b):
		default: // 订阅者跟不上就丢，它下次重新 GET 会拿到全量
		}
	}
}

func (r Role) String() string { return string(r) }
