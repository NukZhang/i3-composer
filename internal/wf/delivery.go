package wf

import (
	"context"
	"fmt"

	"github.com/dapr/durabletask-go/workflow"
)

// RetryPolicy 是 activity 重试策略的别名，避免每个调用点都 import durabletask。
type RetryPolicy = workflow.RetryPolicy

// Delivery 是一条 backlog 的交付编排：生成制品 → 收敛 → 提交 → 测试 → 构建 →
// 【机器到这里收手】。打 tag 与发布都不在 workflow 里：
//
//   - 打 tag 是人在验证页的显式动作，BFF 会重新校验闸门后落账；
//   - 上哪个环境、和谁一起上，在项目级「发布编排」里定。
//
// 这个函数必须纯粹：所有 IO 都在 activity 里。v3.2 起它没有需要等人工的地方，
// 所以也不会挂起——六步跑完就结束，进程随时可以死。
func Delivery(ctx *workflow.WorkflowContext) (any, error) {
	var in Input
	if err := ctx.GetInput(&in); err != nil {
		return nil, fmt.Errorf("解码输入失败：%w", err)
	}

	// 每一步的幂等键 = instanceID/step。Dapr 重放同一 activity 时 OpID 不变。
	op := func(step int) string { return fmt.Sprintf("%s/%d", ctx.ID(), step) }

	steps := []string{ActGenArtifacts, ActConverge, ActCommit, ActTest, ActBuild}

	for i, name := range steps {
		in2 := StepInput{Input: in, Step: i, OpID: op(i)}
		var res StepResult
		t := ctx.CallActivity(name, workflow.WithActivityInput(in2), workflow.WithActivityRetryPolicy(retry))
		if err := t.Await(&res); err != nil {
			return nil, fmt.Errorf("步骤 %d（%s）执行失败：%w", i+1, name, err)
		}
		if !res.OK {
			// 业务拒绝：不是异常，是流程受阻。落一条 blocked 事实，然后正常结束——
			// 人修好后重新发起一个新的实例，而不是让旧实例无限重试。
			b := BlockInput{StepInput: StepInput{Input: in, Step: i, OpID: op(i) + "/blocked"},
				Reason: res.Detail, By: "i3-pipeline"}
			_ = ctx.CallActivity(ActBlocked, workflow.WithActivityInput(b)).Await(nil)
			return map[string]any{"ok": false, "step": i, "reason": res.Detail}, nil
		}
	}

	// 收口：写「可以交付了」这条事实，工作项停在「打 tag」。
	const stepVerify = 5
	v := StepInput{Input: in, Step: stepVerify, OpID: op(stepVerify)}
	var vr StepResult
	if err := ctx.CallActivity(ActVerified, workflow.WithActivityInput(v)).Await(&vr); err != nil {
		return nil, fmt.Errorf("记录交付完成失败：%w", err)
	}
	if !vr.OK {
		b := BlockInput{StepInput: StepInput{Input: in, Step: stepVerify, OpID: op(stepVerify) + "/blocked"},
			Reason: vr.Detail, By: "i3-pipeline"}
		_ = ctx.CallActivity(ActBlocked, workflow.WithActivityInput(b)).Await(nil)
		return map[string]any{"ok": false, "step": stepVerify, "reason": vr.Detail}, nil
	}
	return map[string]any{"ok": true, "verified": true, "commit": vr.CommitNo}, nil
}

// Answer 是人通过卡片 / 工作台给出的发布决定。
type Answer struct {
	Approve bool   `json:"approve"`
	By      string `json:"by"`
	Note    string `json:"note,omitempty"`
}

// Register 把 workflow 与 activity 注册到 Dapr。
//
// 注册名与 CallActivity / StartWorkflow 用的字符串必须一致：
// 用显式名字（Add*N）而不是反射名，否则改个函数名就把历史实例全打断了。
// activity 把一个「context + 类型化入参」的业务函数适配成 durabletask 的
// Activity 签名。适配层只做解码，不放任何判断——判断永远在 domain 或业务函数里。
func activity[In any](fn func(context.Context, In) (StepResult, error)) workflow.Activity {
	return func(actx workflow.ActivityContext) (any, error) {
		var in In
		if err := actx.GetInput(&in); err != nil {
			return nil, fmt.Errorf("解码 activity 入参失败：%w", err)
		}
		return fn(actx.Context(), in)
	}
}

func Register(r *workflow.Registry, a *Activities) error {
	return RegisterAs(r, a, NameDelivery)
}

// RegisterAs 用指定名字注册同一套 workflow。
//
// 名字不同 = Dapr 里是不同的 actor 类型：两套流程可以并存，互不抢工作项。
// 同一套 Dapr 上跑多个实例时用它隔开。
func RegisterAs(r *workflow.Registry, a *Activities, name string) error {
	if name == "" {
		name = NameDelivery
	}
	if err := r.AddWorkflowN(name, Delivery); err != nil {
		return err
	}
	acts := []struct {
		name string
		fn   workflow.Activity
	}{
		{ActGenArtifacts, activity(a.GenArtifacts)},
		{ActConverge, activity(a.Converge)},
		{ActCommit, activity(a.Commit)},
		{ActTest, activity(a.Test)},
		{ActBuild, activity(a.Build)},
		{ActDeploy, activity(a.Deploy)},
		{ActPublish, activity(a.Publish)},
		{ActVerified, activity(a.Verified)},
		{ActWaiting, activity(a.Waiting)},
		{ActBlocked, activity(a.Blocked)},
	}
	for _, x := range acts {
		if err := r.AddActivityN(x.name, x.fn); err != nil {
			return err
		}
	}
	return nil
}
