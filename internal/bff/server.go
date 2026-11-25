package bff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	path "path"
	"strings"
	"sync"
	"time"

	"github.com/zhangkun/i3-composer/internal/agentexec"
	"github.com/zhangkun/i3-composer/internal/dapr"
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

	mu   sync.Mutex
	subs map[chan string]struct{} // SSE 订阅者（进程内即可：断线重连是常态）
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
	m.HandleFunc("POST /api/items/{id}/answer", s.wrap(s.answer))
	m.HandleFunc("POST /api/role", s.wrap(s.setRole))
	m.HandleFunc("GET /api/timeline/stream", s.stream)

	// v2.3：交付单元 / BRD / Agent / 服务验收 / 批注
	m.HandleFunc("GET /api/units", s.wrap(s.units))
	m.HandleFunc("POST /api/units", s.wrap(s.createUnit))
	m.HandleFunc("POST /api/units/{id}/svcverify", s.wrap(s.svcVerify))
	m.HandleFunc("GET /api/brd", s.wrap(s.brd))
	m.HandleFunc("POST /api/brd", s.wrap(s.saveBRD))
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
	// web/dist 是 Vite 构建产物：必须从仓库根起跑（见 docs/SPEC-frontend-wiring.md）。
	m.Handle("/", spa(http.Dir("web/dist")))
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
func spa(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" || strings.Contains(path.Ext(p), ".") {
			files.ServeHTTP(w, r)
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
}

func toTask(it *store.Item) Task {
	return Task{
		ID: it.ID, Title: it.Title, PT: it.PT, St: it.St, Owner: it.Owner, Kind: it.Kind,
		PBC: it.PBC, Type: it.Type, Mode: it.Mode, Env: it.Env, Run: it.Run, Step: it.Step,
		Steps: it.Steps, Version: it.Version, Tag: it.Tag, URL: it.URL, By: it.By,
		Blocked: it.Blocked, BlockedBy: it.BlockedBy, At: it.At,
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

// overview 是工作台首页：待办 / 待确认 / 我发起的 + 项目进度。
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

// tasks 是「我的待办 / 待我确认 / 我发起的」三个过滤视图。
//
// 过滤口径与原型一致：待确认 = 到了发布闸（run=waiting），谁看见谁拍；
// 但真正能不能拍，看的是 /answer 里的权限判定，这里只是展示。
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
		Title string `json:"title"`
		Type  string `json:"type"`
		Env   string `json:"env"`
		Mode  string `json:"mode"`
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

	// 编号在全项目范围内取：需求池是各单元工作项合起来看的，
	// 各单元各自从 US-01 开始会出现两个 US-01，谁也说不清指哪一条。
	id, err := s.nextGlobalItemID(r, c.PBC)
	if err != nil {
		return err
	}
	it := store.Item{
		ID: id, Title: in.Title, PT: 3, St: "todo", Owner: "—", Kind: "Story",
		PBC: c.PBC, Type: in.Type, Mode: in.Mode, Env: in.Env, Run: "idea",
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
	writeJSON(w, http.StatusOK, map[string]any{"item": toTask(it), "facts": facts})
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

	// Ledger 跟着工作项走：工作项落到哪个进程执行是 Dapr 决定的，
	// 账本前缀必须由发起方带过去，不能由执行方自己猜。
	in := wf.Input{ItemID: it.ID, PBC: it.PBC, Title: it.Title, Type: it.Type, Env: it.Env, Mode: it.Mode,
		Ledger: s.Log.Prefix()}
	inst := s.instanceID(it.ID)
	err = s.Dapr.StartWorkflow(r.Context(), s.Comp, s.wfName(), inst, in)
	if errors.Is(err, dapr.ErrConflict) {
		return fmt.Errorf("%w：%s 已有一个未结束的实例", errBadRequest, c.ItemID)
	}
	if err != nil {
		return err
	}
	// 先记一条起始事实，让投影立刻显示 running，不必等 activity 回来。
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemRunStarted, c.Role.String(),
		map[string]any{"id": it.ID}); err != nil {
		return err
	}
	s.publish(c.PBC, "item.run.started", map[string]any{"id": it.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": it.ID, "instance": inst})
	return nil
}

// answer 是人类拍板的唯一入口：同意 / 拒绝发布。
//
// 权限判定只在这里做，且只看会话角色——请求体里的 role / canApprove 一律不读。
// 决定本身只是一个事件，送给挂起的 workflow；它不直接改任何状态。
func (s *Server) answer(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActTag); err != nil {
		return err
	}
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	it, ok := v.Items[c.ItemID]
	if !ok {
		return fmt.Errorf("%w：%s", errNotFound, c.ItemID)
	}
	if it.Run != "waiting" {
		return fmt.Errorf("%w：%s 不在发布闸（run=%s）", errBadRequest, c.ItemID, it.Run)
	}

	// 把决定记成事实（先落账，再唤醒），再作为外部事件送给 workflow。
	// 顺序不能反：先唤醒后落账，若落账失败，人看到「已发布」但账本里没有。
	payload := map[string]any{"id": it.ID, "approve": in.Approve, "by": string(c.Role), "note": in.Note}
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactPublishDecided, string(c.Role), payload); err != nil {
		return err
	}
	ans := wf.Answer{Approve: in.Approve, By: string(c.Role), Note: in.Note}
	if err := s.Dapr.RaiseEvent(r.Context(), s.Comp, s.instanceID(it.ID), wf.EventPublish, ans); err != nil {
		return err
	}
	s.publish(c.PBC, "publish.decided", map[string]any{"id": it.ID, "approve": in.Approve})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": it.ID, "approve": in.Approve})
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

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
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
