package wf

import (
	"context"
	"fmt"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/workflow"
)

// NameProbe 是启动自检专用的 workflow 名（#60）。
//
// 为什么需要它：sidecar 没有「列出注册表」的接口，只能靠「发起一个实例、
// 看它跑没跑起来」来探测。可正常的 Delivery 一发起就真干活（生成制品、写账、提交），
// 拿它当探针就是往账本里灌脏数据。所以注册一个只回一句话的 workflow：
// 跑得起来 = 这个名字已经被 worker 认领。
const NameProbe = "i3-probe"

// Probe 只回一句话，不碰任何外部状态，重放也安全。
func Probe(ctx *workflow.WorkflowContext) (any, error) {
	return "probe ok", nil
}

// RegisterProbe 把探针 workflow 用 name 注册。
func RegisterProbe(r *workflow.Registry, name string) error {
	if name == "" {
		name = NameProbe
	}
	return r.AddWorkflowN(name, Probe)
}

// ProbeClient 是启动自检要用到的客户端方法（durabletask client 与测试替身都满足）。
type ProbeClient interface {
	ScheduleNewWorkflow(ctx context.Context, name string, opts ...api.NewWorkflowOptions) (api.InstanceID, error)
	WaitForWorkflowCompletion(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)
}

// CheckRegistered 发起一次探针实例并等它跑完：返回 nil 表示名字在册。
//
// 名字没注册时 Dapr 不会报错，而是照建实例、再以 FAILED +
// 「workflow named 'X' is not registered」收场——这里把这句话原样交回去，
// 启动方据此拒绝启动，而不是带着一个永远发起不了的名字对外服务。
// 连不上 sidecar 同样是 error：查不了就报查不了，不猜（宪法 II）。
func CheckRegistered(ctx context.Context, c ProbeClient, name, instanceID string) error {
	if name == "" {
		name = NameProbe
	}
	// api.WithInstanceID 与 workflow.WithInstanceID 是同一种选项，只是包不同。
	if _, err := c.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(api.InstanceID(instanceID))); err != nil {
		return fmt.Errorf("发起探针实例失败：%w", err)
	}
	// 失败时错误里带的是 sidecar 的原话（「workflow named 'X' is not registered」）；
	// 只要终态不是 COMPLETED，都算这个名字没被认领。
	meta, err := c.WaitForWorkflowCompletion(ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
	if err != nil {
		return fmt.Errorf("等探针实例 %s 结束失败：%w", instanceID, err)
	}
	if meta == nil {
		return fmt.Errorf("探针实例 %s 查不到终态", instanceID)
	}
	if meta.RuntimeStatus != api.RUNTIME_STATUS_COMPLETED {
		return fmt.Errorf("探针实例 %s 没跑完（%v）：%v", instanceID, meta.RuntimeStatus, meta)
	}
	return nil
}
