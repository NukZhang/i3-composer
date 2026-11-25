package deployer

import (
	"os/exec"
	"testing"
	"time"
)

// 进程是自己起的，就得由自己收。
//
// 早先的版本只在换版本/回滚时停旧进程，服务退出时不收：
// 每跑一轮验收就在机器上多留一个，服务一停全成孤儿。
func TestStopAllReapsProcesses(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("环境里没有 sleep，跳过")
	}
	d := New()
	cmd := exec.Command(sleep, "300")
	if err := cmd.Start(); err != nil {
		t.Skip("起不了进程，跳过")
	}
	p := &proc{inst: &Instance{PBC: "fee", Env: "test", PID: cmd.Process.Pid}, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	d.mu.Lock()
	d.running[key("fee", "test")] = p
	d.mu.Unlock()

	if !d.Alive("fee", "test") {
		t.Fatal("刚起的进程应算活着")
	}
	d.StopAll()
	if d.Alive("fee", "test") {
		t.Fatal("StopAll 之后不该还活着")
	}
	if len(d.Running()) != 0 {
		t.Fatalf("StopAll 之后不该还有登记：%+v", d.Running())
	}
	// 真的收掉了，不只是从表里删了一条。读到值的 done 就是 reaper 的 Wait
	// 已返回的凭据——Wait 返回意味着进程被回收，不会留僵尸。
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("StopAll 之后进程没有被 wait 回收")
	}
}

// Alive 要问操作系统，不能拿 ProcessState 当判据——
// 那个字段只有 Wait 之后才填，进程自己崩了它也还是 nil。
func TestAliveNoticesProcessDeath(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("环境里没有 sleep，跳过")
	}
	d := New()
	cmd := exec.Command(sleep, "0") // 立刻退出
	if err := cmd.Start(); err != nil {
		t.Skip("起不了进程，跳过")
	}
	d.mu.Lock()
	p := &proc{inst: &Instance{PBC: "fee", Env: "test", PID: cmd.Process.Pid}, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	d.running[key("fee", "test")] = p
	d.mu.Unlock()
	defer d.StopAll()

	time.Sleep(300 * time.Millisecond)
	if d.Alive("fee", "test") {
		t.Fatal("进程已经退出，Alive 不该说它活着")
	}
}
