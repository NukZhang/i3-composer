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

	"github.com/zhangkun/i3-composer/internal/artifacts"
	"github.com/zhangkun/i3-composer/internal/bff"
	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/pipeline"
	"github.com/zhangkun/i3-composer/internal/seed"
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
		workDir  = env("WORK_DIR", "")                   // 执行侧工作区（空则用系统临时目录）
		artDir   = env("ARTIFACT_DIR", "")               // 产物仓库（空则用 ./.i3-artifacts）
		ledger   = env("LEDGER_PREFIX", "")              // 账本前缀：同一套 Dapr 上跑隔离账本时用
		wfName   = env("WORKFLOW_NAME", wf.NameDelivery) // workflow 名：不同名 = 不同 actor 类型
	)

	dc := dapr.New(httpEnd)
	log := store.New(dc, stateSt, ledger)

	// logFor 按前缀给出一本账。
	//
	// 工作项落到哪个进程执行是 Dapr 决定的，所以账本前缀必须由发起方
	// 带进工作项里，activity 再据此选账本——不能靠进程自己那份。
	logFor := func(prefix string) *store.Store {
		if prefix == ledger {
			return log
		}
		return store.New(dc, stateSt, prefix)
	}

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
	// 执行侧：真生成制品、真跑测试、真构建、真部署。
	// 以前这里是 mockBuilder——digest 是树摘要拼的，测试条数是编的，
	// 部署 URL 是拼出来的。现在这一层真的干活。
	exec, err := pipeline.New(log, workDir)
	if err != nil {
		return fmt.Errorf("初始化执行侧失败：%w", err)
	}
	if artDir != "" {
		exec.Store = artifacts.New(artDir)
	}
	exec.LogFor = logFor
	acts := &wf.Activities{D: wf.Deps{
		Log:    log,
		Pub:    publisher{dc: dc, pubsub: pubsub},
		Exec:   exec,
		LogFor: logFor,
	}}
	if err := wf.RegisterAs(registry, acts, wfName); err != nil {
		return fmt.Errorf("注册 workflow 失败：%w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// worker 必须在建实例前连上 sidecar，否则 start 会因「没有 worker」失败。
	go func() {
		slog.Info("workflow worker 启动", "grpc", grpcEnd, "workflow", wfName)
		// StartWorker 阻塞到 ctx 结束；它一旦返回就说明流断了。
		if err := wfc.StartWorker(ctx, registry); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow worker 退出", "err", err)
		}
	}()

	if seedDemo {
		// 种子要真的构建一次基线：digest 得是真的构建产物的摘要，
		// 不是拿树摘要拼一个假的 sha256:baseline。
		so := seed.Options{Build: func(ctx context.Context, pbc, commit string, files map[string]string) (string, bool, error) {
			out, err := exec.Build(ctx, pbc, commit, files)
			if err != nil {
				return "", false, err
			}
			return out.Digest, out.Binary, nil
		}, Test: func(ctx context.Context, pbc, commit string, files map[string]string) (int, int, []string, error) {
			out, err := exec.Test(ctx, pbc, commit, files)
			return out.Pass, out.Total, out.Failures, err
		}}
		if err := seed.Run(context.Background(), log, so); err != nil {
			slog.Warn("种子数据写入失败", "err", err)
		}
	}
	srv := bff.NewServer(log, dc, bff.NewSessions([]byte(sessKey)), wfComp)
	srv.WfName = wfName
	// 发布批次的部署与回滚都用执行侧同一套东西：构建产物在哪、进程归谁收，
	// 不能有两份实现。
	srv.Pipe = exec
	srv.LocalPort = bff.PortOfAddr(addr)
	// 启动就把 pubsub 投递地址核对一遍：sidecar 只往它的 app-port 投事件，
	// 本实例监听别的端口时工作流照跑、事件一条也收不到，验收里极难看出原因。
	srv.Pubsub = srv.CheckPubsubWiring(context.Background())
	if w, ok := srv.Pubsub["ok"].(bool); ok && !w {
		slog.Warn("pubsub 投递地址不一致", "detail", srv.Pubsub["detail"])
	} else if d, ok := srv.Pubsub["detail"].(string); ok {
		slog.Info("pubsub 投递地址", "detail", d)
	}
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
	// 部署起的进程归我们收：不停掉的话，进程会在机器上赖着不走。
	exec.StopAll()
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
