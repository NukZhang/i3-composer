// Package deployer 是真实的部署：把构建产物跑起来，等它真的健康了才算部署成功。
//
// 以前的 Deploy 拼一个假 URL 就写一条 deploy.succeeded 事实，
// 于是「已部署」这件事没有任何东西可以验证。这里的部署是真的：
//
//   - 起进程（构建产物的二进制），监听端口由进程自己决定
//   - 轮询 /healthz，直到真的返回 200
//   - 记录真实 URL、端口、进程号；换版本时先停旧进程
//   - 回滚 = 停掉进程，端口释放
//
// 它不假装自己是 Kubernetes：这里是本机进程部署，接口留着，
// 接真集群时换掉这一层实现即可（上层契约不变）。
package deployer

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Instance 是一个正在跑的部署。
type Instance struct {
	PBC     string `json:"pbc"`
	Env     string `json:"env"`
	Commit  string `json:"commit"`
	Digest  string `json:"digest"`
	URL     string `json:"url"`
	Health  string `json:"health"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

// Deployer 管理本机上的部署实例：同 (pbc, env) 只有一个活着的。
// 不同 env 各自登记、各自活着（#19）：收的时候只收指定的那一份，不连坐别的环境。
type Deployer struct {
	mu       sync.Mutex
	running  map[string]*proc
	HealthTO time.Duration
	// BinaryDir 是部署时的可执行文件目录（构建产物已落地）。
	BinaryDir string
}

type proc struct {
	inst *Instance
	cmd  *exec.Cmd
	// done 在进程退出且被 Wait 回收之后关闭。
	// 它是「还活着吗」的唯一可靠判据：进程自己崩了，只有 Wait 能知道。
	done chan struct{}
}

// New 造一个部署器。
func New() *Deployer {
	return &Deployer{running: map[string]*proc{}, HealthTO: 30 * time.Second}
}

// listenLine 匹配进程打印的 `listen=127.0.0.1:34567`。
var listenLine = regexp.MustCompile(`listen=(\S+)`)

// Deploy 启动构建产物，等它健康，返回真实实例。
//
// binary 是构建产物的路径；它必须来自 build.succeeded 事实指向的那个文件。
func (d *Deployer) Deploy(ctx context.Context, pbc, env, commit, digest, binary string) (*Instance, error) {
	// 同 (pbc, env) 换版本：先把旧的那个让出来。注意这里只能摘自己那一份——
	// 走 Stop 的话，它「没找到就按 pbc 兜底」的分支会把同一单元**别的环境**
	// 刚起来的进程一起收掉（#19：SIT 刚上线，UAT 一部署它就死了）。
	// 端口是每个进程自己挑的（ADDR=127.0.0.1:0），不同环境的进程不会抢端口。
	old := d.take(key(pbc, env))
	if old != nil {
		slog.Info("同 (pbc, env) 换版本，先停旧的", "pbc", pbc, "env", env, "pid", old.inst.PID)
		stopProc(old)
	}

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "ADDR=127.0.0.1:0", "ENV="+env, "COMMIT="+commit)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动进程失败：%w", err)
	}

	addrCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := listenLine.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case addrCh <- m[1]:
				default:
				}
			}
		}
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(10 * time.Second):
		kill(cmd)
		return nil, fmt.Errorf("进程没有报告监听地址")
	case <-ctx.Done():
		kill(cmd)
		return nil, ctx.Err()
	}

	inst := &Instance{
		PBC: pbc, Env: env, Commit: commit, Digest: digest,
		URL: "http://" + addr, PID: cmd.Process.Pid,
		Started: time.Now().Format("01-02 15:04:05"),
	}
	if err := d.waitHealth(ctx, inst.URL); err != nil {
		kill(cmd)
		return nil, err
	}
	inst.Health = "200 OK"

	// 注册前先把「谁负责 Wait」定下来：进程退出由这个 goroutine 发现并回收，
	// 不靠信号探测（僵尸进程对信号 0 也会应答，探测不出它已经死了）。
	p := &proc{inst: inst, cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()

	d.mu.Lock()
	// 登记的同一刻就派一个哨兵盯着 reaper：进程自己退出/被杀之后，这张表必须跟着清掉。
	// 不这样做的话，表里留着一个已经死掉的 proc，「同 (pbc, env) 只有一个活着的」就成了假话：
	// 回滚 Stop 收的是这个死条目，真正占着端口的那次部署（同 pbc、另一个 env 键）就漏掉了（#41 自测遇过）。
	// 顺带把 PID 落日志：进程没被收掉时，日志里能直接看到是哪一个。
	slog.Info("部署进程已起", "pbc", pbc, "env", env, "pid", inst.PID, "url", inst.URL, "key", key(pbc, env))
	// 登记之前再看一眼：Deploy 开头停旧进程之后、登记之前，可能已经有同一个键的新进程登记进来
	// （并发发布同一个单元）。直接覆盖会让先登记的那个变成没人认领的孤儿。
	// 注意这里也不能走 Stop：这条路径上另一个环境可能刚登记好，按 pbc 兜底会把它一起收掉（#19）。
	prev, had := d.running[key(pbc, env)]
	d.running[key(pbc, env)] = p
	d.mu.Unlock()
	if had && prev != nil && prev != p {
		slog.Warn("同一 (pbc, env) 已有登记的进程，覆盖前先收掉旧的", "pbc", pbc, "env", env, "旧pid", prev.inst.PID, "新pid", inst.PID)
		stopProc(prev)
	}
	go func() {
		<-p.done
		d.mu.Lock()
		if d.running[key(pbc, env)] == p {
			delete(d.running, key(pbc, env))
			slog.Info("部署进程自行退出，已从登记表移除", "pbc", pbc, "env", env, "pid", inst.PID)
		}
		d.mu.Unlock()
	}()
	return inst, nil
}

// kill 杀掉进程并回收它。
//
// 只 Kill 不 Wait 会留下僵尸：进程已经死了，但没被 wait 过的子进程
// 会一直挂在父进程的名下（ps 里是 defunct）。
// 只用在「还没交给 proc 的 reaper」的那些路径上。
func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

// stopProc 停掉一个已登记的进程，并等它的 reaper 收完。
func stopProc(p *proc) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
	}
}

// waitHealth 轮询健康检查，直到 200 或超时。
//
// 部署成功的判据是「它真的能应答」，不是「进程起来了」。
func (d *Deployer) waitHealth(ctx context.Context, base string) error {
	to := d.HealthTO
	if to <= 0 {
		to = 30 * time.Second
	}
	deadline := time.Now().Add(to)
	client := &http.Client{Timeout: 3 * time.Second}
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code == http.StatusOK {
				return nil
			}
			last = "健康检查返回 " + strconv.Itoa(code)
		} else {
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("健康检查超时（%s）", last)
}

// take 从登记表里摘掉一个键对应的进程（不收）。摘不到返回 nil。
func (d *Deployer) take(k string) *proc {
	d.mu.Lock()
	p := d.running[k]
	delete(d.running, k)
	d.mu.Unlock()
	return p
}

// Stop 停掉某个 (pbc, env) 的部署进程。回滚走这里。
func (d *Deployer) Stop(pbc, env string) {
	k := key(pbc, env)
	d.mu.Lock()
	p := d.running[k]
	delete(d.running, k)
	d.mu.Unlock()
	if p == nil {
		// 「回滚了但进程还在应答」比「多杀一个」严重，所以这一下按 pbc 兜底：同 pbc 名下
		// 还活着的部署都收掉。但要说是谁按的——并发发布同一个单元到别的环境时，
		// 这一下收掉的正是那个环境**刚起来**的进程（#19：SIT 刚 201，UAT 一失败它就死了）。
		// 正常回滚的调用方先找准自己的 (pbc, env)，不会走到这里，所以这里如实记 WARN。
		var extra []*proc
		d.mu.Lock()
		for kk, pp := range d.running {
			if strings.HasPrefix(kk, pbc+"/") {
				extra = append(extra, pp)
				delete(d.running, kk)
			}
		}
		d.mu.Unlock()
		for _, pp := range extra {
			slog.Warn("Stop 没找到该 (pbc, env) 的登记，按 pbc 兜底收掉", "pbc", pbc, "env", env,
				"foundEnv", pp.inst.Env, "pid", pp.inst.PID)
			stopProc(pp)
		}
		slog.Info("Stop：登记表里没有对应进程", "pbc", pbc, "env", env, "兜底收掉", len(extra))
		return
	}
	stopProc(p)
	select {
	case <-p.done:
		slog.Info("Stop：已收掉部署进程", "pbc", pbc, "env", env, "pid", p.inst.PID)
	case <-time.After(3 * time.Second):
		slog.Error("Stop：进程 3s 内没收掉", "pbc", pbc, "env", env, "pid", p.inst.PID)
	}
}

// StopAll 停掉所有还在跑的部署。
//
// 进程是这个部署器起的，就得由它收。服务退出时不收，
// 每次验收都会在机器上留一堆没人管的进程——部署「成功」了，
// 产物却赖着不走。
func (d *Deployer) StopAll() {
	d.mu.Lock()
	ps := make([]*proc, 0, len(d.running))
	for _, p := range d.running {
		ps = append(ps, p)
	}
	d.running = map[string]*proc{}
	d.mu.Unlock()
	for _, p := range ps {
		stopProc(p)
	}
}

// Running 列出当前活着的部署。
func (d *Deployer) Running() []Instance {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Instance, 0, len(d.running))
	for _, p := range d.running {
		out = append(out, *p.inst)
	}
	return out
}

// Alive 判定某个部署是否还在跑。
//
// 判据是 reaper 有没有收完，不是 ProcessState，也不是信号探测：
// ProcessState 只有 Wait 之后才填；而僵尸进程对信号 0 照样应答，
// 探测不出它已经死了。
func (d *Deployer) Alive(pbc, env string) bool {
	d.mu.Lock()
	p := d.running[key(pbc, env)]
	d.mu.Unlock()
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func key(pbc, env string) string { return pbc + "/" + env }

// ProbeHealth 探测一个 URL 现在是否还健康。发布页的「还活着吗」用它。
func ProbeHealth(ctx context.Context, base string) (int, string) {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/healthz", nil)
	if err != nil {
		return 0, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Status
}
