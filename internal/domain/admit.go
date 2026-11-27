package domain

import (
	"fmt"
	"strings"
)

// 本文件是「入口准入」：工作项进流水线之前，先问五件事。
//
// 问题出在只有结果侧有门。提交、测试、构建、验收四道闸都在流水线末端，
// 入口是敞开的：一条只写了标题的 story 也能发起，机器照着标题生成一套
// 模板制品，跑到「提交」才因为引用不闭合 blocked——错误发现得太晚，
// 而 block 的理由还是执行器猜的，不是入口判的。
//
// 五项里三项硬、两项软：
//
//	硬：目标可断言 / 数据与权限 / 责任人 —— 不过就拒绝，一次说清「缺什么 / 找谁 / 补哪」；
//	软：范围边界 / 上下文齐备 —— 不过放行，但要把「哪项没补」落成事实，页面挂黄条。
//
// 门槛不该把人卡死在入口（软项），也不该假装判过了（unknown）。
//
// 三条判定口径，都是宪法 II「判不了就说判不了」：
//   - 账本里没有的（库表权限开没开），判不了就返回 unknown，不画绿勾；
//   - 机器生成的模板正文（PRD / AT 由 X 团队照标题产出）不算人的目标断言，
//     只认 source=手动 的那一版；拿模板冒充验收口径，等于把「优化一下」
//     自动翻译成五条 Given/When/Then，那不是断言，是回音；
//   - 人该给却没给的（验收口径、数据范围、责任人），那是「缺」，不是「判不了」，
//     明确拒掉并点名。
//
// 「硬」在这里的含义不是「我们一定能判」，而是「没给就不许进」：
// 数据范围可以判不了，但不能连「要碰哪些表」都没说。

// 三项判定结论。unknown 不是「通过」，也不是「不通过」——它是没证据。
const (
	AdmitPass    = "pass"
	AdmitFail    = "fail"
	AdmitUnknown = "unknown"
)

// AdmitInput 是准入的判据集合。
//
// 它刻意全是值：调用方（BFF）负责从账本把事实读出来填进去，
// 这里不碰 IO、不读时钟，所以同样的输入必然得到同样的判定，可以单测。
type AdmitInput struct {
	ID     string
	Title  string
	Type   string // new | feature | defect | backlog | doc
	Kind   string // Story | Task
	Parent string // 挂在哪个 story 下；空 = 顶层

	// Accept 是这条工作项自己写的验收口径（建单时给），每条应是
	// Given/When/Then 或「当……则……」。它是入口最直接的目标断言。
	Accept []string
	// Data 是本次要碰的库表 / 字段（如 fee.bill.amount）。
	// 权限开没开本系统查不到，但「要碰什么」必须由人写明。
	Data []string
	// AcceptBy 是验收人，ComplyBy 是合规背责人。两者都要有人。
	AcceptBy string
	ComplyBy string

	// HumanAcceptText / HumanAcceptFrom 是单元里人写下的验收口径正文
	// （AT 或 PRD 里 source=手动 的最新一版）。生成的不算，理由见文件头。
	HumanAcceptText string
	HumanAcceptFrom string

	// ADText 是单元最新一版 AD 的正文（没有就是空串）。只在「数据与权限」
	// 一项里用来核对：声明的表在 AD 里点不到，说明范围与设计对不上。
	ADText string

	// HasBacklog 是「单元已有 Backlog 制品」；HasTaskBreakdown 是
	// 「这条 Story 已经拆到 task」。它们是软项的证据。
	HasBacklog       bool
	HasTaskBreakdown bool
}

// AdmitCheck 是一项检查的结论。
//
// Missing/Ask/Fix 只在没过时有值：缺什么、找谁、补哪——三行话，
// 而不是一句「400 请求不合法」。
type AdmitCheck struct {
	Key     string `json:"k"`
	Label   string `json:"n"`
	Hard    bool   `json:"hard"`
	Verdict string `json:"st"` // pass | fail | unknown
	Detail  string `json:"d,omitempty"`
	Missing string `json:"missing,omitempty"`
	Ask     string `json:"ask,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// AdmitResult 是准入的判定结果。
//
// Pass 只看硬项 fail：硬项都给了就放行，unknown 不算拦阻（否则永远进不来）。
// Waivers 只有「软项不过」——那是人该补、但不该因此被挡在门口的事，页面挂黄条。
// unknown 留在 Checks 里，页面按 verdict 显示「判不了」；它不是黄条的理由，
// 因为那类缺口（授权账本还没接）不是提交人现在能补的。
// Reasons 是硬项不过的那几项，原样进 400 与事实。
type AdmitResult struct {
	Pass    bool
	Checks  []AdmitCheck
	Waivers []string
	Reasons []AdmitCheck
}

// Admit 判一条工作项能不能进流水线。
//
// 顺序有意义：目标说不清的时候，「数据范围」和「责任人」写得再全
// 也只是把错的事做快，所以目标排在最前，也是 400 里最先被念出来的那条。
func Admit(in AdmitInput) AdmitResult {
	checks := []AdmitCheck{
		admitTarget(in),
		admitData(in),
		admitOwner(in),
		admitScope(in),
		admitContext(in),
	}
	res := AdmitResult{Checks: checks, Pass: true}
	for _, c := range checks {
		switch {
		case c.Verdict == AdmitFail && c.Hard:
			res.Pass = false
			res.Reasons = append(res.Reasons, c)
		case c.Verdict == AdmitFail: // 软项不过：放行，但记进 waiver
			res.Waivers = append(res.Waivers, fmt.Sprintf("%s：%s", c.Label, c.Missing))
		}
	}
	return res
}

// admitTarget 判「目标可断言」。
//
// 判据只有「能不能被真跑一遍」：验收口径要么是 Given/When/Then，
// 要么是「当……则……」。只说「优化一下」「快一点」的不是目标，是愿望。
// 先看这条工作项自己写的口径（这次要验的是什么）；没有才退到单元里
// 人写下的 AT / PRD 验收标准（模板生成的不算）。
func admitTarget(in AdmitInput) AdmitCheck {
	c := AdmitCheck{Key: "target", Label: "目标可断言", Hard: true}
	if acc := cleanList(in.Accept); len(acc) > 0 {
		var bad []string
		for _, a := range acc {
			if !assertable(a) {
				bad = append(bad, a)
			}
		}
		if len(bad) > 0 {
			return badTarget(c, fmt.Sprintf("验收口径里有 %d 条不可断言（%s）", len(bad), strings.Join(bad, "；")))
		}
		c.Verdict = AdmitPass
		c.Detail = fmt.Sprintf("工作项写了 %d 条可断言的验收口径", len(acc))
		return c
	}
	if txt := strings.TrimSpace(in.HumanAcceptText); txt != "" && hasGWT(txt) {
		from := in.HumanAcceptFrom
		if from == "" {
			from = "验收口径"
		}
		c.Verdict = AdmitPass
		c.Detail = fmt.Sprintf("用单元里人工写下的%s（含 Given/When/Then）", from)
		return c
	}
	return badTarget(c, "这条工作项没写验收口径，单元里也没有人写下的验收标准（生成的模板不算）")
}

func badTarget(c AdmitCheck, missing string) AdmitCheck {
	c.Verdict = AdmitFail
	c.Missing = missing
	c.Ask = "BA 业务"
	c.Fix = "在这条工作项上补一句验收口径：当……则……（或 Given/When/Then），能被真跑一遍的那种"
	return c
}

// admitData 判「数据与权限」。
//
// 两件事分开判，因为它们能判的程度不一样：
//   - 「要碰哪些库表 / 字段」是工作项必须声明的，没声明就是缺，硬拒；
//   - 「权限开没开」账本里根本没有这条线（配置页的连接器是演示数据），
//     所以声明了也只能返回 unknown，等授权账本落账再来画绿勾。
//
// 有 AD 时顺手核一遍：声明的表在 AD 的设计里点不到，说明数据范围
// 和设计对不上，这一条也不能算「判过了」。
func admitData(in AdmitInput) AdmitCheck {
	c := AdmitCheck{Key: "data", Label: "数据与权限", Hard: true}
	tables := cleanList(in.Data)
	if len(tables) == 0 {
		c.Verdict = AdmitFail
		c.Missing = "没说要碰哪些库表 / 字段"
		c.Ask = "SA 系统"
		c.Fix = "在这条工作项上补数据范围（如 fee.bill.amount），SA 按它开权限"
		return c
	}
	c.Verdict = AdmitUnknown
	decl := fmt.Sprintf("声明了 %d 项数据范围（%s）", len(tables), strings.Join(tables, "、"))
	if strings.TrimSpace(in.ADText) == "" {
		c.Detail = decl + "；单元还没有 AD 可以对，权限开没开本系统也查不到——判不了"
	} else if miss := notInText(tables, in.ADText); len(miss) > 0 {
		c.Detail = decl + fmt.Sprintf("；AD 里点不到 %s，数据范围和设计对不上——判不了", strings.Join(miss, "、"))
	} else {
		c.Detail = decl + "，AD 里都点得到；但权限开没开本系统查不到——判不了"
	}
	c.Ask = "SA 系统"
	c.Fix = "让 AD 落一版、由 SA 按 AD 开库表权限；授权账本接入后这一项才能画绿勾"
	return c
}

// admitOwner 判「责任人」：谁验、谁背合规的责。两个都不能空。
//
// 「—」「内部」这类占位不算人：账本里已经有一堆 owner=— 的行，
// 那次是渲染用的占位，不是责任人。
func admitOwner(in AdmitInput) AdmitCheck {
	c := AdmitCheck{Key: "owner", Label: "责任人", Hard: true}
	var miss []string
	if !isPerson(in.AcceptBy) {
		miss = append(miss, "验收人")
	}
	if !isPerson(in.ComplyBy) {
		miss = append(miss, "合规背责人")
	}
	if len(miss) > 0 {
		c.Verdict = AdmitFail
		c.Missing = "缺 " + strings.Join(miss, " / ")
		c.Ask = "验收人找 BA 业务；合规背责人找 SA 系统"
		c.Fix = "在这条工作项上填验收人（acceptBy）与合规背责人（complyBy），写人名或岗位名都行"
		return c
	}
	c.Verdict = AdmitPass
	c.Detail = fmt.Sprintf("验收人 %s · 合规背责人 %s", strings.TrimSpace(in.AcceptBy), strings.TrimSpace(in.ComplyBy))
	return c
}

// admitScope 判「范围边界」（软）：涉及哪几个单元、要不要跨系统。
//
// 证据是「有没有拆到 task」：一条顶层 Story 没拆解就发起，范围只能靠
// 机器按标题猜，那正是入口该提示、但不该拦死的东西。
func admitScope(in AdmitInput) AdmitCheck {
	c := AdmitCheck{Key: "scope", Label: "范围边界", Hard: false}
	switch {
	case in.HasTaskBreakdown:
		c.Verdict = AdmitPass
		c.Detail = "已拆到 task，范围落在可执行的工作项上"
	case in.Type == "doc" || in.Type == "backlog":
		c.Verdict = AdmitPass
		c.Detail = "变更类型只动文档 / 待办，不涉及服务边界"
	default:
		c.Verdict = AdmitFail
		c.Missing = "没拆到 task，涉及哪几个单元、要不要跨系统没落到账上"
		c.Ask = "PA 产品"
		c.Fix = "在项目页把这条 Story 拆成 task；或确认它只动一个单元，然后在描述里写清"
	}
	return c
}

// admitContext 判「上下文齐备」（软）：关联的 AD / 契约 / 历史工单挂上没有。
func admitContext(in AdmitInput) AdmitCheck {
	c := AdmitCheck{Key: "context", Label: "上下文齐备", Hard: false}
	switch {
	case strings.TrimSpace(in.Parent) != "":
		c.Verdict = AdmitPass
		c.Detail = "挂在 story " + in.Parent + " 下，上下文随父项走"
	case in.HasBacklog:
		c.Verdict = AdmitPass
		c.Detail = "单元已有 Backlog 制品，可对照历史工作项与验收口径"
	default:
		c.Verdict = AdmitFail
		c.Missing = "没有关联的 AD / 外部契约 / 历史工单"
		c.Ask = "BA 业务"
		c.Fix = "把关联的 AD、外部契约或历史工单挂上来；单元先有一版 Backlog 也算"
	}
	return c
}

// dropGWTPhrase 去掉「把 Given/When/Then 当名词提一句」的写法。
//
// 这是个真坑：模板 PRD 的验收口径写的是「见 AT 制品：每条 Given/When/Then
// 都要能被真跑一遍」——它同时含 given 与 then，光看关键词会把它当成
// 「有可断言的口径」。把这一整个并列短语先抹掉，再判前后置，
// 模板就成了「没有口径」，人写的「当……则……」不受影响。
func dropGWTPhrase(s string) string {
	r := strings.NewReplacer(
		"given/when/then", "", "given、when、then", "", "given when then", "",
		"given-when-then", "",
	)
	return r.Replace(s)
}

// assertable 判一句话能不能被真跑一遍。
//
// 判据来自 AT 制品的固定写法：Given/When/Then，中文对应「当……则……」。
// 要求同时有前置和可观察的结论——只写了「优化一下」的句子两边都没有。
// 长度下限 8 个字是为了挡掉「修一下」「快点」这种连动作都没说清的。
func assertable(s string) bool {
	t := dropGWTPhrase(strings.ToLower(strings.TrimSpace(s)))
	if len([]rune(t)) < 8 {
		return false
	}
	has := func(xs ...string) bool {
		for _, x := range xs {
			if strings.Contains(t, x) {
				return true
			}
		}
		return false
	}
	premise := has("given", "当", "前置", "如果")
	outcome := has("then", "则", "应", "返回", "要能", "不得", "就")
	return premise && outcome
}

// hasGWT 判一段人工写下的验收标准里有没有一条可断言的条目。
//
// 按行判：整段只要有一行「当……则……」或「Given……Then……」就够；
// 那一行本身还要过 assertable 的长度与前后置检查，避免把一句
// 「见 AT 制品：每条 Given/When/Then……」当成口径。
func hasGWT(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if assertable(line) {
			return true
		}
	}
	return false
}

// isPerson 判一个名字是不是真有人认领。「—」「-」「」「内部」是占位。
func isPerson(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "—", "-", "–", "内部", "待定", "TBD":
		return false
	}
	return true
}

// notInText 返回在 text 里点不到的项。用于「声明的数据范围 vs AD 的设计」。
func notInText(xs []string, text string) []string {
	var miss []string
	for _, x := range xs {
		if !strings.Contains(text, x) {
			miss = append(miss, x)
		}
	}
	return miss
}

// cleanList 去掉空白项，保留原顺序。空串不是「声明了一项」。
func cleanList(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s := strings.TrimSpace(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}
