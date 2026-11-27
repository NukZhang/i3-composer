package bff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zhangkun/i3-composer/internal/dapr"
	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// TestRealChainEndToEnd 需要真实 Dapr 和独立账本。测试进程须和应用共享网络，
// 才能直接核对发布批次启动的进程。E2E_BASE、DAPR_HTTP_ENDPOINT 和
// LEDGER_PREFIX 要指向同一个隔离实例。
func TestRealChainEndToEnd(t *testing.T) {
	base := strings.TrimRight(os.Getenv("E2E_BASE"), "/")
	endpoint := os.Getenv("DAPR_HTTP_ENDPOINT")
	prefix := os.Getenv("LEDGER_PREFIX")
	if base == "" || endpoint == "" || prefix == "" {
		t.Skip("需要隔离实例：设置 E2E_BASE / DAPR_HTTP_ENDPOINT / LEDGER_PREFIX")
	}

	ctx := t.Context()
	log := store.New(dapr.New(endpoint), "statestore", prefix)
	client := &http.Client{Timeout: 15 * time.Second}
	roles := make(map[Role][]*http.Cookie)
	post := func(path string, payload any, role Role, want int) map[string]any {
		t.Helper()
		if _, ok := roles[role]; !ok {
			body, err := json.Marshal(map[string]Role{"role": role})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Post(base+"/api/role", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			roles[role] = resp.Cookies()
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || len(roles[role]) == 0 {
				t.Fatalf("切换 %s 角色返回 HTTP %d", role, resp.StatusCode)
			}
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range roles[role] {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("%s 返回 HTTP %d，响应解析失败：%v", path, resp.StatusCode, err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s 返回 HTTP %d，预期 %d：%v", path, resp.StatusCode, want, out)
		}
		return out
	}
	load := func(unit string) (*store.View, []store.Fact) {
		t.Helper()
		facts, err := log.Load(ctx, unit)
		if err != nil {
			t.Fatal(err)
		}
		view, err := store.Project(unit, facts)
		if err != nil {
			t.Fatal(err)
		}
		return view, facts
	}

	proj, _ := post("/api/projects", map[string]string{"name": "端到端验收"}, RolePA, http.StatusCreated)["id"].(string)
	unit := "e2e" + fmt.Sprint(time.Now().UnixNano())
	post("/api/units", map[string]string{"id": unit, "name": "端到端验收单元", "kind": "pbc", "proj": proj}, RolePA, http.StatusCreated)
	query := "?pbc=" + unit
	post("/api/artifacts"+query, map[string]string{
		"artifact": "at",
		"text":     "- AT-01 构建质量。Given 交付已完成，When 查看质量报告，Then 测试全通过且构建提供摘要",
	}, RoleBA, http.StatusCreated)
	item, _ := post("/api/tasks"+query, map[string]any{
		"title": "核对端到端交付", "type": "new", "env": "test", "mode": "fast",
		"accept": []string{"当交付完成时，应能核对已通过的测试和构建摘要"},
		"data":   []string{"fee.bill.amount"}, "acceptBy": "BA 业务", "complyBy": "SA 系统",
	}, RoleBA, http.StatusCreated)["id"].(string)
	itemPath := "/api/items/" + item
	post(itemPath+"/run"+query, nil, RoleDA, http.StatusAccepted)

	var view *store.View
	var facts []store.Fact
	for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		view, facts = load(unit)
		if it := view.Items[item]; it != nil && (it.Run == "blocked" || it.Run == "verified") {
			break
		}
	}
	if it := view.Items[item]; it == nil || it.Run != "verified" || it.Step != 5 || len(view.Commits) == 0 {
		t.Fatalf("工作项应完成五步并待打 tag：%+v", it)
	}
	head := view.Commits[0]
	code, _ := domain.Find(view.Versions, domain.Code, head.Tree[domain.Code])
	for _, name := range []string{"go.mod", "main.go", "main_test.go"} {
		if len(code.Files[name]) == 0 {
			t.Errorf("源码制品缺少 %s", name)
		}
	}
	if v := view.Test; v == nil || v.Commit != head.No || v.Total < 1 || v.Pass != v.Total {
		t.Fatalf("当前提交需要真实的测试结果：%+v", v)
	}
	if !strings.HasPrefix(head.Digest, "sha256:") || len(head.Digest) != len("sha256:")+64 {
		t.Fatalf("当前提交需要编译产物摘要，实际 %q", head.Digest)
	}
	for _, f := range facts {
		switch f.Type {
		case store.FactItemWaiting, store.FactPublishDecided, store.FactItemPublished, store.FactDeploySucceeded:
			t.Fatalf("五步结束后尚未进入项目发布，出现 %s", f.Type)
		}
	}
	t.Logf("工作项 %s 完成五步，提交 %s，测试 %d/%d，摘要 %s", item, head.No, view.Test.Pass, view.Test.Total, head.Digest)

	post(itemPath+"/answer"+query, map[string]bool{"approve": true}, RoleSA, http.StatusNotFound)
	if after, _ := load(unit); after.Seq != view.Seq {
		t.Fatalf("退役入口不得写账本：%d → %d", view.Seq, after.Seq)
	}
	t.Logf("退役入口返回 HTTP 404，账本仍停在 seq %d", view.Seq)

	post("/api/checks/AT-01/decide"+query, map[string]string{"commit": head.No, "st": "pass"}, RoleBA, http.StatusCreated)
	tag := post("/api/units/"+unit+"/tag", nil, RoleSA, http.StatusCreated)
	if tag["commit"] != head.No || tag["digest"] != head.Digest {
		t.Fatalf("tag 必须固化当前提交和摘要：%v", tag)
	}
	batch := post("/api/projects/"+proj+"/orch/release", map[string]any{
		"env": "SIT", "mode": "full", "units": []string{unit},
	}, RoleSA, http.StatusCreated)
	deployed, _ := load(unit)
	if len(deployed.Deploys) != 1 || deployed.Deploys[0].Tag != tag["tag"] || deployed.Deploys[0].Commit != head.No {
		t.Fatalf("部署事实应能追溯批次锁定的 tag 和提交：%+v", deployed.Deploys)
	}
	url := deployed.Deploys[0].URL
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("部署地址应指向本机测试进程：%q", url)
	}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查返回 HTTP %d", resp.StatusCode)
	}
	t.Logf("项目批次 %v 锁定 %v，部署事实与 /healthz HTTP 200 一致", batch["id"], tag["tag"])
	post("/api/projects/"+proj+"/orch/rollback", map[string]any{"id": batch["id"]}, RoleSA, http.StatusOK)
}
