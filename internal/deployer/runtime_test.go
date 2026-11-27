package deployer

import "testing"

func TestStopRuntimeDoesNotStopReplacement(t *testing.T) {
	manager := New()
	slot := key("dun", "SIT")
	manager.running[slot] = &proc{inst: &Instance{RuntimeID: "replacement"}, done: make(chan struct{})}
	if manager.StopRuntime("dun", "SIT", "previous") {
		t.Fatal("旧实例标识不应停止新进程")
	}
	if current, ok := manager.Instance("dun", "SIT"); !ok || current.RuntimeID != "replacement" {
		t.Fatalf("新部署应仍然登记：%+v，%v", current, ok)
	}
	if !manager.StopRuntime("dun", "SIT", "replacement") {
		t.Fatal("匹配的实例应被移除")
	}
	if _, ok := manager.Instance("dun", "SIT"); ok {
		t.Fatal("被停止的实例不应仍然登记")
	}
}
