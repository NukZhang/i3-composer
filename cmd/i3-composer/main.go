// i3-composer 是原型 i3-composer v2.1 的可运行内核：
//
//	L4  本进程的 /api（BFF 防腐层：协议翻译、会话权限、投影出口）
//	L3  internal/domain（纯判断）+ internal/store（事实日志，唯一权威）
//	    internal/wf（Dapr Workflow 编排：派活 / 等待 / 重试 / 超时）
//	L2  Dapr sidecar 的 durable execution（workflow 引擎 + actors）
//	L1  模型网关：不在本仓库，harness 直连
//
// 本进程只做业务；持久化、定时器、重放、并发串行化全部交给 Dapr。
// 不需要 dapr CLI：只要一个 sidecar 监听 HTTP 3500 / gRPC 50001 即可。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dapr/durabletask-go/workflow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/zhangkun/i3-composer/internal/bff"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
	"github.com/zhangkun/i3-composer/internal/wf"
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		slog.Error("退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		httpEnd  = env("DAPR_HTTP_ENDPOINT", "http://127.0.0.1:3500")
		grpcEnd  = env("DAPR_GRPC_ENDPOINT", "127.0.0.1:50001")
		stateSt  = env("STATE_STORE", "statestore")
		pubsub   = env("PUBSUB", "pubsub")
		wfComp   = env("WORKFLOW_COMPONENT", "dapr")
		addr     = env("ADDR", ":8080")
		sessKey  = os.Getenv("SESSION_KEY")
		seedDemo = env("SEED_DEMO", "1") == "1"
	)

	dc := dapr.New(httpEnd)
	log := store.New(dc, stateSt)

	// 与 sidecar 建一条 gRPC 连接，用于注册并运行 workflow worker。
	// 这条连接只承载 work-item stream，业务 API 全走上面的 HTTP。
	conn, err := grpc.NewClient(grpcEnd,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("连接 sidecar gRPC %s 失败：%w", grpcEnd, err)
	}
	defer conn.Close()

	wfc := workflow.NewClient(conn)
	registry := workflow.NewRegistry()
	acts := &wf.Activities{D: wf.Deps{
		Log:   log,
		Pub:   publisher{dc: dc, pubsub: pubsub},
		Build: mockBuilder{},
	}}
	if err := wf.Register(registry, acts); err != nil {
		return fmt.Errorf("注册 workflow 失败：%w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// worker 必须在建实例前连上 sidecar，否则 start 会因「没有 worker」失败。
	go func() {
		slog.Info("workflow worker 启动", "grpc", grpcEnd, "workflow", wf.NameDelivery)
		// StartWorker 阻塞到 ctx 结束；它一旦返回就说明流断了。
		if err := wfc.StartWorker(ctx, registry); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow worker 退出", "err", err)
		}
	}()

	if seedDemo {
		if err := seed(context.Background(), log); err != nil {
			slog.Warn("种子数据写入失败", "err", err)
		}
	}

	srv := bff.NewServer(log, dc, bff.NewSessions([]byte(sessKey)), wfComp)
	// 每一次事实落链（包括 workflow activity 写的）都通知 SSE：前端收到后重拉全量投影。
	// 这样「页面自动推进」不依赖某个 handler 是否记得调 publish。
	log.Notify = srv.NotifyChange
	hs := &http.Server{
		Addr:              addr,
		Handler:           withCORS(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("BFF 监听", "addr", addr, "stateStore", stateSt, "workflowComponent", wfComp)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http 服务异常", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("收尾中…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shutCtx)
}

// publisher 把 activity 的事件发到 Dapr pub/sub。占位 topic 由组件 routing 决定去向。
type publisher struct {
	dc     *dapr.Client
	pubsub string
}

func (p publisher) Publish(ctx context.Context, topic string, data any) error {
	return p.dc.Publish(ctx, p.pubsub, topic, data)
}

// mockBuilder 就是原型里唯一标「大」的那块：真接 CI（Jenkins）要替换它。
// 故意不假装打通了——digest 由树内容确定性派生，便于回放与断言。
type mockBuilder struct{}

func (mockBuilder) Build(_ context.Context, pbc, commit string, tree domain.Tree) (string, error) {
	return "sha256:" + domain.TreeSHA(tree)[7:] + "-" + commit + "@" + pbc, nil
}

// seed 给一个可演示的起点：一个 PBC、一条已发布的工作项、一条停在发布闸的。
// 只在日志为空时写，保证可重复执行不产生重复事实。
func seed(ctx context.Context, log *store.Store) error {
	// Ensure 必须在任何 Append 之前：空日志没有 etag，并发写会互相覆盖。
	if err := log.Ensure(ctx, "fee"); err != nil {
		return err
	}
	facts, err := log.Load(ctx, "fee")
	if err != nil {
		return err
	}
	if len(facts) > 0 {
		return nil
	}
	if _, err := log.Append(ctx, "fee", store.FactPBCCreated, "PA", map[string]string{"name": "悦府物业 · 收费与工单"}); err != nil {
		return err
	}

	// 制品基线与基线提交：原型里 fee 这个 PBC 是有历史的（v1.0.1 起就已入树），
	// 所以一个 feature 变更只需重算受影响的那几件，其余沿用树里的版本。
	// 少了这段基线，任何一次提交都会因「backlog 未入树」被引用闭合闸拒绝——
	// 这是闸的正确行为，不是 bug。
	if err := seedBaseline(ctx, log, "fee"); err != nil {
		return err
	}

	items := []store.Item{
		{ID: "US-01", Title: "业主账单自助查询与支付", PT: 5, St: "done", Owner: "DA",
			Kind: "Story", PBC: "fee", Type: "new", Mode: "guided", Env: "test",
			Run: "published", Version: "1.0.2", Tag: "v1.0.0", By: "BA"},
		{ID: "US-02", Title: "管家批量生成缴费单", PT: 3, St: "doing", Owner: "DA",
			Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
			Run: "waiting", Step: 6, By: "PA"},
		{ID: "US-03", Title: "报修工单 SLA 超时提醒", PT: 3, St: "todo", Owner: "BA",
			Kind: "Story", PBC: "fee", Type: "feature", Mode: "fast", Env: "test",
			Run: "idea", By: "BA"},
	}
	for _, it := range items {
		if it.Steps == nil {
			it.Steps = store.StepPlan(it.Env)
		}
		for i := range it.Steps {
			switch it.Run {
			case "published":
				it.Steps[i].St = "done"
				it.Steps[i].At = "11-22 15:18"
			case "waiting":
				if i < 6 {
					it.Steps[i].St = "done"
					it.Steps[i].At = "11-22 15:18"
				}
			}
		}
		if _, err := log.Append(ctx, "fee", store.FactItemCreated, it.By, it); err != nil {
			return err
		}
	}
	return nil
}

// seedBaseline 写 v1.0.1 的制品线与基线提交，对应原型 PBC.fee 的历史。
//
// 为什么必须有它：交付链路的「引用闭合」闸要求树里六件制品齐备，且每件的
// 衍生引用都指向树里同一版本。树是从版本线取 latest 得来的，所以没有基线版本
// 的制品在树上就是空串 → 闸拒绝。同时 Commit 用「有无草稿」判定要不要提交，
// 基线已 accepted，不会被误当成新变更。
func seedBaseline(ctx context.Context, log *store.Store, pbc string) error {
	const at = "11-20 17:20"
	vers := []domain.Version{
		{T: domain.PRD, V: "1.0.1", At: at, By: "BA", Source: "手动", St: domain.StAccepted, Note: "收费与工单基线"},
		{T: domain.Proto, V: "1.0.1", At: at, By: "x-ui", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}}, Note: "账单列表与缴费页"},
		{T: domain.AD, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.Proto, V: "1.0.1"}}, Note: "单库设计"},
		{T: domain.AT, V: "1.0.1", At: at, By: "x-qa", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.PRD, V: "1.0.1"}, {T: domain.AD, V: "1.0.1"}}, Note: "4 条验收用例"},
		{T: domain.Backlog, V: "1.0.1", At: at, By: "x-ba", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}}, Note: "3 story / 9 task"},
		{T: domain.Code, V: "1.0.1", At: at, By: "x-fe", Source: "生成", St: domain.StAccepted,
			DF: []domain.Ref{{T: domain.AT, V: "1.0.1"}, {T: domain.Backlog, V: "1.0.1"}}, Note: "基线代码"},
	}
	for _, v := range vers {
		op := "seed/baseline/" + v.T.String()
		if _, err := log.AppendOp(ctx, pbc, op, store.FactArtifactDrafted, v.By, v); err != nil {
			return err
		}
	}

	tree := domain.Tree{}
	for _, t := range domain.Order {
		tree[t] = "1.0.1"
	}
	c := domain.Commit{
		No: "1.0.1", Tree: tree, By: "张昆", At: at,
		Msg: "基线：PRD 手动上传 + 三件制品生成", Type: "new",
		TreeSHA: domain.TreeSHA(tree),
		Digest:  "sha256:baseline", // 基线镜像已存在，避免「未构建无 digest」闸挡住演示
	}
	_, err := log.AppendOp(ctx, pbc, "seed/baseline/commit", store.FactCommitCreated, "i3-pipeline", c)
	return err
}

// withCORS 允许原型页（file:// 或另一端口）直接调这套 API。
// 只放行 GET/POST + JSON，不带凭据通配——会话靠同源 cookie，跨源时前端改用 /api/role 拿角色。
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}
