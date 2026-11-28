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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dapr/durabletask-go/client"
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

func resolveWebDir() (string, error) {
	dir, err := filepath.Abs(env("WEB_DIR", "web/dist"))
	if err != nil {
		return "", fmt.Errorf("解析 WEB_DIR 失败：%w", err)
	}
	indexPath := filepath.Join(dir, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return "", fmt.Errorf("无法读取前端入口 %q，请先构建前端或设置 WEB_DIR：%w", indexPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("前端入口 %q 不是普通文件", indexPath)
	}
	index, err := os.Open(indexPath)
	if err != nil {
		return "", fmt.Errorf("无法打开前端入口 %q：%w", indexPath, err)
	}
	defer index.Close()
	return dir, nil
}

func waitForPubsubWiring(ctx context.Context, srv *bff.Server) (map[string]any, error) {
	for {
		wiring := srv.CheckPubsubWiring(ctx)
		if wiring["checked"] == true && wiring["ok"] == true {
			return wiring, nil
		}
		if wiring["checked"] == true || srv.LocalPort == 0 {
			return nil, fmt.Errorf("REQUIRE_PUBSUB=1：%v", wiring["detail"])
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("REQUIRE_PUBSUB=1：%v（%w）", wiring["detail"], ctx.Err())
		case <-timer.C:
		}
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

	webDir, err := resolveWebDir()
	if err != nil {
		return err
	}
	dc := dapr.New(httpEnd)
	log := store.New(dc, stateSt, ledger)

	srv := bff.NewServer(log, dc, bff.NewSessions([]byte(sessKey)), wfComp)
	srv.PrototypeRenderSocket = os.Getenv("PROTOTYPE_RENDER_SOCKET")
	srv.WebDir = webDir
	srv.WfName = wfName
	srv.LocalPort = bff.PortOfAddr(addr)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if env("REQUIRE_PUBSUB", "0") == "1" {
		wiringCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		srv.Pubsub, err = waitForPubsubWiring(wiringCtx, srv)
		cancel()
		if err != nil {
			return err
		}
	}

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
	// 启动自检直接用底层 client：探针要「发起 + 等结束 + 看终态」三步，
	// 这正是它在 gRPC 上做的事。
	probe := client.NewTaskHubGrpcClient(conn, nil)
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
	// 探针与业务 workflow 注册在同一个 registry 里：worker 能不能认领名字，
	// 看的就是这个 registry。
	if err := wf.RegisterProbe(registry, wf.NameProbe); err != nil {
		return fmt.Errorf("注册启动自检 workflow 失败：%w", err)
	}

	// worker 必须在建实例前连上 sidecar，否则 start 会因「没有 worker」失败。
	go func() {
		slog.Info("workflow worker 启动", "grpc", grpcEnd, "workflow", wfName)
		// StartWorker 阻塞到 ctx 结束；它一旦返回就说明流断了。
		if err := wfc.StartWorker(ctx, registry); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow worker 退出", "err", err)
		}
	}()

	// 启动自检放在种子与监听之前：名字没被认领时不写任何数据、也不对外服务（#60）。
	if err := checkWiring(ctx, dc, probe, wfName); err != nil {
		return err
	}

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
		// 种子跑在 sidecar 起来之后：拿不到账本就一直重试到 ctx 结束，
		// 而不是打一条 warn 就带着空账本对外服务（#43 验证时遇过：sidecar 晚到，
		// 进程照常监听，整轮验收对着空库跑）。
		if err := seed.Run(ctx, log, so); err != nil && ctx.Err() == nil {
			slog.Warn("种子数据写入失败", "err", err)
		}
	}
	// 发布批次的部署与回滚都用执行侧同一套东西：构建产物在哪、进程归谁收，
	// 不能有两份实现。
	srv.Pipe = exec
	// 启动就把 pubsub 投递地址核对一遍：sidecar 只往它的 app-port 投事件，
	// 本实例监听别的端口时工作流照跑、事件一条也收不到，验收里极难看出原因。
	if srv.Pubsub == nil {
		srv.Pubsub = srv.CheckPubsubWiring(ctx)
	}
	if w, ok := srv.Pubsub["ok"].(bool); ok && !w {
		slog.Warn("pubsub 投递地址不一致", "detail", srv.Pubsub["detail"])
	} else if d, ok := srv.Pubsub["detail"].(string); ok {
		slog.Info("pubsub 投递地址", "detail", d)
	}
	// 每一次事实落链（包括 workflow activity 写的）都通知 SSE：前端收到后重拉全量投影。
	// 这样「页面自动推进」不依赖某个 handler 是否记得调 publish。
	log.Notify = srv.NotifyChange
	hs := &http.Server{
		Addr: addr,
		// CORS 只对 CORS_ORIGINS 里显式列出的来源放行；默认空 = 不发 CORS 头（#42）。
		Handler:           bff.NewCORS(srv.Handler(), bff.ParseOrigins(env("CORS_ORIGINS", ""))),
		ReadHeaderTimeout: 10 * time.Second,
	}
	var listenErr error
	go func() {
		slog.Info("BFF 监听", "addr", addr, "stateStore", stateSt, "workflowComponent", wfComp)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http 服务异常", "err", err)
			listenErr = err
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("收尾中…")
	// 部署起的进程归我们收：不停掉的话，进程会在机器上赖着不走。
	exec.StopAll()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutCtx); err != nil {
		return err
	}
	// 监听失败（例如端口被占）也是启动失败：以前这里返回 nil、进程退出码 0，
	// 脚本看见「正常退出」就以为服务起过了（#60 自测时遇过）。
	return listenErr
}

// publisher 把 activity 的事件发到 Dapr pub/sub。占位 topic 由组件 routing 决定去向。
type publisher struct {
	dc     *dapr.Client
	pubsub string
}

func (p publisher) Publish(ctx context.Context, topic string, data any) error {
	return p.dc.Publish(ctx, p.pubsub, topic, data)
}

// checkWiring 确认本进程注册的 workflow 名字已经被 sidecar 认领（#60）。
//
// 注册表挂在 sidecar 的 app-id 名下：换了 WORKFLOW_NAME 却没换 --app-id 时，
// 新名字永远不在册，每次发起都只得到「is not registered」，账本上看起来像业务
// 失败，整轮验收全挂。这里用一次探针实例问清楚：跑不通就拒绝启动，
// 并报出 app-id、名字与改法——不用人去猜是 worker 没起还是名字没注册。
func checkWiring(ctx context.Context, dc *dapr.Client, c wf.ProbeClient, wfName string) error {
	// sidecar 的 /v1.0/metadata 顶层 id 是 app-id（没有 worker 连上来时也读得到）。
	appID := "?"
	if ac, err := dc.AppConnection(ctx); err == nil && ac.AppID != "" {
		appID = ac.AppID
	}
	// 等一小会儿：sidecar 刚起时注册表可能还没跟上 worker（最多等 5 秒）。
	id := fmt.Sprintf("i3probe_%d", time.Now().UnixNano())
	var err error
	for i := 0; i < 10; i++ {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = wf.CheckRegistered(pctx, c, wf.NameProbe, id)
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("启动自检被中断（%w）", ctx.Err())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("启动自检没通过：workflow 名 %q 在 sidecar（app-id=%s）上跑不起来：%v。"+
			"本进程按 %s 注册了 worker，但发起实例时没有被认领，对外服务也只会得到同样的错误。"+
			"排查顺序：① sidecar（DAPR_GRPC_ENDPOINT=%s / DAPR_HTTP_ENDPOINT）是否跟本进程连的是同一个；"+
			"② WORKFLOW_NAME 是否被改过——换新名字必须同时把 sidecar 的 --app-id 换成新名字重起（注册表挂在 app-id 名下）。",
			wfName, appID, err, wfName, env("DAPR_GRPC_ENDPOINT", "127.0.0.1:50001"))
	}
	slog.Info("启动自检通过", "workflow", wfName, "probe", wf.NameProbe, "app-id", appID)
	return nil
}
