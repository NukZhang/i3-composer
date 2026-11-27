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

func TestProjectReleasesRollbackIsScopedToProject(t *testing.T) {
	var facts []Fact
	for _, project := range []string{"p1", "p2"} {
		facts = append(facts, releaseFact(t, FactReleaseBatch, ReleaseBatch{
			ID: "REL-1", Proj: project, Env: "SIT", Units: []ReleaseUnit{{ID: project + "-unit"}},
		}))
	}
	facts = append(facts, releaseFact(t, FactReleaseRollback, map[string]any{"id": "REL-1", "proj": "p1"}))
	view, err := ProjectReleases(facts)
	if err != nil {
		t.Fatal(err)
	}
	if view.Batches[0].Status != "active" || view.Batches[1].Status != "rolledback" {
		t.Fatalf("只应回滚项目 p1 的批次，实际 %+v", view.Batches)
	}
}
