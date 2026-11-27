package domain

import "testing"

func TestChecksFromAT(t *testing.T) {
	md := "# 材料入库\n\n- AT-01 实际入库。Given 有计划，When 提交单据，Then 余量减少。\n\n" +
		"### AC-02 余量不足\n- Given: 余量为零\n- When: 提交单据\n- Then: 拒绝请求\n\n" +
		"```md\n- AT-03 示例 Given X When Y Then Z\n```"
	checks := ChecksFromAT(md)
	if len(checks) != 2 {
		t.Fatalf("应有两条真实验收项：%+v", checks)
	}
	if checks[0] != (Check{ID: "AT-01", N: "实际入库", G: "有计划", W: "提交单据", T: "余量减少", ST: CheckTodo}) {
		t.Errorf("行内清单解析错误：%+v", checks[0])
	}
	if checks[1] != (Check{ID: "AC-02", N: "余量不足", G: "余量为零", W: "提交单据", T: "拒绝请求", ST: CheckTodo}) {
		t.Errorf("分行清单解析错误：%+v", checks[1])
	}
	for _, invalid := range []string{
		"# 验收计划\n具体条目待补",
		md + "\n- AT-04 只有前提 Given 有计划",
		md + "\n- AT-01 重复编号 Given 有计划 When 执行 Then 校验",
	} {
		if got := ChecksFromAT(invalid); len(got) != 0 {
			t.Errorf("不完整清单不能用于验收：%+v", got)
		}
	}
}
