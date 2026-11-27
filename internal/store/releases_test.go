package store

import (
	"encoding/json"
	"testing"
)

func releaseFact(t *testing.T, typ string, payload any) Fact {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Fact{Type: typ, Payload: b}
}

func TestProjectReleases(t *testing.T) {
	facts := []Fact{
		releaseFact(t, FactReleaseBatch, map[string]any{
			"id": "REL-1", "proj": "p1", "env": "SIT", "mode": "full", "by": "SA", "at": "11-25 10:00",
			"units": []map[string]any{
				{"id": "dun", "name": "催缴策略", "kind": "pbc", "tag": "v2.0.0", "commit": "1.0.1", "digest": "sha256:a", "binary": true, "url": "http://x", "status": "deployed"},
				{"id": "qa", "name": "交付质检 Agent", "kind": "agent", "tag": "v1.0.0", "commit": "1.0.1", "digest": "sha256:b", "binary": false, "status": "activated"},
			},
		}),
		releaseFact(t, FactReleaseRollback, map[string]any{"id": "REL-1", "by": "SA", "at": "11-25 11:00"}),
		releaseFact(t, FactReleaseBatch, map[string]any{
			"id": "REL-2", "proj": "p1", "env": "生产", "mode": "gray",
			"units": []map[string]any{{"id": "dun", "name": "催缴策略", "tag": "v2.0.0"}},
		}),
	}
	v, err := ProjectReleases(facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Batches) != 2 {
		t.Fatalf("批次应新的在前、共 2 个：%+v", v.Batches)
	}
	if v.Batches[0].ID != "REL-2" {
		t.Errorf("第一个应是 REL-2：%s", v.Batches[0].ID)
	}
	b := v.Batches[1]
	if b.Status != "rolledback" || b.RollbackAt == "" {
		t.Errorf("REL-1 应记为已回滚：%+v", b)
	}
	if len(b.Units) != 2 || b.Units[1].URL != "" {
		t.Errorf("Agent 产物不该有 URL：%+v", b.Units)
	}
	if v.Batches[0].Status != "active" {
		t.Errorf("REL-2 应是 active：%s", v.Batches[0].Status)
	}
}

func TestProjectReleasesEmpty(t *testing.T) {
	v, err := ProjectReleases(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Batches == nil {
		t.Fatal("空账本应给空数组而不是 null（前端要直接 map）")
	}
	if len(v.Batches) != 0 {
		t.Fatalf("不应有批次：%+v", v.Batches)
	}
}

// progress 要按单元认领，不能按「第一个还没 URL 的」填（#41 自测发现）：
// 批次里 Agent 排在前面时（它不部署、不发 progress），PBC 的地址会被填到 Agent 名下，
// 真正部署的那个单元读出来 URL 是空——回滚按 URL 判空跳过它，进程就留在机器上了。
func TestReleaseProgressClaimsNamedUnit(t *testing.T) {
	rv, err := ProjectReleases([]Fact{
		releaseFact(t, FactReleaseBatch, map[string]any{
			"id": "REL-9", "proj": "p1", "env": "SIT", "mode": "full",
			"units": []map[string]any{
				{"id": "qa", "kind": "agent", "binary": false},
				{"id": "dun", "kind": "pbc", "binary": true},
			},
		}),
		releaseFact(t, FactReleaseProgress, map[string]any{"id": "REL-9", "unit": "dun", "url": "http://dun", "at": "x"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	b := rv.Batches[0]
	if b.Units[0].URL != "" || b.Units[0].Status == "deployed" {
		t.Fatalf("progress 不该落到没部署的 Agent 上：%+v", b.Units[0])
	}
	if b.Units[1].URL != "http://dun" || b.Units[1].Status != "deployed" {
		t.Fatalf("PBC 应认领自己的部署地址：%+v", b.Units[1])
	}
}
