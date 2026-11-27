package bff

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/zhangkun/i3-composer/internal/store"
)

// reconcileRuns 向 Dapr 对账「还在跑」的工作项，并把失败补落成事实（#53）。
//
// 为什么在读取侧做：item.run.started 是发起方写的，而 workflow 失败时
// 往往连 activity 都跑不起来（sidecar 断、实例没注册），没有执行侧能可靠地回来写一句。
// Dapr 的实例终态是唯一权威（宪法 I），所以在读之前问它一次：
// 终态不是「还在跑」的，就把这一轮失败落账，之后投影自然变成 failed。
//
// 一条事实只落一次：只有投影里 run=running / waiting 的项才会被对账；
// 落过 item.run.failed 之后 run 不再是 running，下一轮读不会再落。
func (s *Server) reconcileRuns(ctx context.Context, pbc string, v *store.View) bool {
	if s.Dapr == nil || v == nil {
		return false
	}
	changed := false
	for id, it := range v.Items {
		if it.Run != "running" && it.Run != "waiting" {
			continue
		}
		inst := s.instanceID(pbc + "_" + id)
		st, err := s.Dapr.GetWorkflow(ctx, s.Comp, inst)
		if err != nil {
			// 查不了就如实留着 running：不拿「查不到」冒充「没失败」（宪法 II）。
			slog.Warn("对账 workflow 终态失败", "item", id, "instance", inst, "err", err)
			continue
		}
		reason := ""
		switch {
		case st == nil:
			reason = fmt.Sprintf("sidecar 查不到实例 %s（可能已重启或数据被清）", inst)
		case st.RuntimeStatus == "FAILED", st.RuntimeStatus == "CANCELLED", st.RuntimeStatus == "TERMINATED":
			reason = st.DaprStatus()
		default:
			continue
		}
		status := "NOT_FOUND"
		if st != nil {
			status = st.RuntimeStatus
		}
		if _, err := s.Log.Append(ctx, pbc, store.FactItemRunFailed, "i3-bff", map[string]any{
			"id": id, "instance": inst, "status": status, "reason": reason,
		}); err != nil {
			slog.Warn("补落 item.run.failed 失败", "item", id, "err", err)
			continue
		}
		it.Run, it.St, it.Failed, it.FailedStatus = "failed", "done", reason, status
		changed = true
	}
	return changed
}

// loadState 是 /api/state 专用读取：先对账，再给投影。
func (s *Server) loadState(r *http.Request, pbc string) (*store.View, error) {
	facts, err := s.Log.Load(r.Context(), pbc)
	if err != nil {
		return nil, err
	}
	v, err := store.Project(pbc, facts)
	if err != nil {
		return nil, err
	}
	if s.reconcileRuns(r.Context(), pbc, v) {
		// 补落了事实：重投影一次，让这一轮读就带上失败，而不是等下一次刷新。
		facts, err = s.Log.Load(r.Context(), pbc)
		if err != nil {
			return nil, err
		}
		return store.Project(pbc, facts)
	}
	return v, nil
}
