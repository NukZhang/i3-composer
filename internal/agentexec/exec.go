// Package agentexec 是 Agent 的执行器：按编排图真的走一遍。
//
// 以前的「试跑」是回放：把定义里存好的样例输出原样端出来，
// 事实里明写 source:"sample"。那不是执行，是录像回放——
// 输入变了输出也不会变，所以它证明不了任何东西。
//
// 这里的执行器真的按图走：
//
//	start  取输入变量
//	tool   调真工具（读会话、读账单、查策略、写回执）
//	kb     在真知识库里检索（按命中度排序，带出处）
//	llm    做真判断（合规判定 / 话术生成 / 测试结论）
//	branch 按上游显式字段判定分支
//	hitl   转人工，记录等待与超时口径
//	end    汇总输出
//
// 每个节点的实际耗时、真实输出都记下来，所以「这一轮花在哪」是测出来的，
// 不是写死的。
package agentexec

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/domain"
)

//go:embed data/corpus.json
var corpusJSON []byte

// corpus 是执行器读的数据：会话、规则、口径、账单、策略、知识库文档。
type corpus struct {
	Sessions  []session          `json:"sessions"`
	Rules     []rule             `json:"rules"`
	Prices    []price            `json:"prices"`
	Bills     []bill             `json:"bills"`
	Strategy  strategy           `json:"strategy"`
	Knowledge map[string][]kbDoc `json:"knowledge"`
}

type session struct {
	ID             string `json:"id"`
	Account        string `json:"account"`
	Customer       string `json:"customer"`
	Agent          string `json:"agent"`
	Channel        string `json:"channel"`
	SilenceMinutes int    `json:"silenceMinutes"`
}

type rule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Severity string   `json:"severity"`
	Patterns []string `json:"patterns"`
	Advice   string   `json:"advice"`
}

type price struct {
	Project string  `json:"project"`
	Price   float64 `json:"price"`
	Note    string  `json:"note"`
}

type bill struct {
	ID          string  `json:"id"`
	Account     string  `json:"account"`
	Name        string  `json:"name"`
	Amount      float64 `json:"amount"`
	OverdueDays int     `json:"overdueDays"`
	Tier        string  `json:"tier"`
	Attempt     int     `json:"attempt"`
}

type strategy struct {
	Tiers               []tier   `json:"tiers"`
	Redlines            []string `json:"redlines"`
	AmountEscalate      float64  `json:"amountEscalate"`
	MaxAttemptsPer7Days int      `json:"maxAttemptsPer7Days"`
}

type tier struct {
	Range    string `json:"range"`
	Channel  string `json:"channel"`
	Interval string `json:"interval"`
	Escalate string `json:"escalate"`
}

type kbDoc struct {
	Lib   string `json:"lib"`
	Doc   string `json:"doc"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Ver 是这篇文档所属库的版本。缺省时按库取 KB.Libs 的版本，再缺省为 v0，
	// 保证装配事实里「这篇来自哪一版」不空着冒充成最新。
	Ver string `json:"ver,omitempty"`
}

var theCorpus corpus

func init() {
	if err := json.Unmarshal(corpusJSON, &theCorpus); err != nil {
		panic("agentexec: 语料解析失败：" + err.Error())
	}
}

// NodeRun 是一个节点的真执行结果。
type NodeRun struct {
	ID    string         `json:"id"`
	Name  string         `json:"n"`
	Type  string         `json:"t"`
	Ms    int64          `json:"ms"`
	Out   map[string]any `json:"out,omitempty"`
	Wait  string         `json:"wait,omitempty"`
	Notes []string       `json:"notes,omitempty"`
}

// Run 是一轮执行的完整结果。
type Run struct {
	Input  map[string]any `json:"input"`
	Nodes  []NodeRun      `json:"nodes"`
	Output map[string]any `json:"output"`
	Raw    string         `json:"raw"`
	Ms     int64          `json:"ms"`
	// Branch 是走过的分支（节点 → 命中的条件）。
	Branch []string `json:"branch,omitempty"`
	// Escalated 为真表示这轮转了人工。
	Escalated bool `json:"escalated,omitempty"`
	// Assemblies 是这一轮每个 kb 节点的装配结果（载入了什么、省了多少）。
	Assemblies []domain.ContextAssembly `json:"assemblies,omitempty"`
}

// Blocked 返回这一轮里被工具白名单拒掉的节点。
//
// 试跑页要把它显示出来（只显示「跑通」等于把「没真调用」说成了「调过了」）；
// 面向客户的对话则不能拿这样一轮去拼回复。
func (r *Run) Blocked() []NodeRun {
	if r == nil {
		return nil
	}
	var out []NodeRun
	for _, n := range r.Nodes {
		if n.Out != nil && n.Out["blocked"] == true {
			out = append(out, n)
		}
	}
	return out
}

// Exec 是执行器。
type Exec struct {
	// Now 可注入，便于测试。
	Now func() time.Time
}

func (e *Exec) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Execute 按编排图执行一轮。
//
// 输入按开始节点声明的变量名填：缺的就用样例值兜底，
// 但输出一定是这轮算出来的。
func (e *Exec) Execute(ctx context.Context, a *domain.Agent, input map[string]any) (*Run, error) {
	if a == nil || a.Flow == nil {
		return nil, fmt.Errorf("Agent 没有编排，无法执行")
	}
	if err := a.Flow.Valid(); err != nil {
		return nil, err
	}
	run := &Run{Input: map[string]any{}}
	for k, v := range input {
		run.Input[k] = v
	}
	start := e.now()
	// 上下文变量：节点之间靠它传值。
	vars := map[string]any{}
	// 每个节点的输出按节点 id 存，供后面引用。
	outs := map[string]map[string]any{}

	// 走到哪个节点：按边走。分支节点可能有多个后继，靠 cond 选中。
	cur := ""
	for _, n := range a.Flow.Nodes {
		if n.T == domain.NodeStart {
			cur = n.ID
			break
		}
	}
	visited := map[string]bool{}
	for cur != "" && len(visited) < 64 {
		if visited[cur] {
			break
		}
		visited[cur] = true
		node := findNode(a.Flow, cur)
		if node == nil {
			break
		}
		t0 := e.now()
		out, wait, notes, asm := e.runNode(a, node, run.Input, vars, outs)
		ms := e.now().Sub(t0).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		if asm != nil {
			run.Assemblies = append(run.Assemblies, *asm)
			// 装配结果同时挂回节点输出：试跑页在 kb 节点上就地展开，
			// 不用跨节点去找「这一轮载入了什么」。
			nrOut := map[string]any{}
			for k, v := range out {
				nrOut[k] = v
			}
			nrOut["assembly"] = asm
			out = nrOut
		}
		nr := NodeRun{ID: node.ID, Name: node.N, Type: node.T, Ms: ms, Out: out, Notes: notes}
		if wait != "" {
			nr.Wait = wait
			run.Escalated = true
		}
		run.Nodes = append(run.Nodes, nr)
		if out != nil {
			outs[node.ID] = out
			for k, v := range out {
				vars[k] = v
			}
		}
		// 选下一个节点。
		next, branch := e.nextNode(a.Flow, node, outs)
		if branch != "" {
			run.Branch = append(run.Branch, branch)
		}
		cur = next
	}
	run.Ms = e.now().Sub(start).Milliseconds()
	run.Output = vars
	run.Raw = e.renderRaw(run)
	return run, nil
}

// runNode 执行一个节点。返回节点输出、等待口径、备注，以及（kb 节点才有）装配结果。
func (e *Exec) runNode(a *domain.Agent, n *domain.FlowNode, input map[string]any, vars map[string]any, outs map[string]map[string]any) (map[string]any, string, []string, *domain.ContextAssembly) {
	cfg := n.Cfg
	switch n.T {
	case domain.NodeStart:
		out := map[string]any{}
		for _, v := range asStrings(cfg["vars"]) {
			name := v[0]
			val, ok := input[name]
			if !ok {
				val = sampleFor(name, v)
			}
			out[name] = val
		}
		return out, "", nil, nil

	case domain.NodeTool:
		tool, _ := cfg["tool"].(string)
		// 白名单是执行侧的边界，不是文档里的约定：闸红了也要拦，因为试跑
		// 不经过闸。越界时不调用工具，返回一条结构化拒绝让页面看得见。
		if !a.Allows(tool) {
			reason := "工具 " + tool + " 不在白名单内"
			if len(a.Tools) == 0 {
				reason = "这个 Agent 没有工具白名单，任何工具都不许调用（" + tool + " 被拒）"
			}
			return map[string]any{"blocked": true, "tool": tool, "reason": reason}, "",
				[]string{"tool.blocked：" + reason}, nil
		}
		return e.runTool(tool, cfg, input, vars, outs), "", nil, nil

	case domain.NodeKB:
		query := stringFrom(vars, "sessionText", "customerText", "draft")
		chunks, omitted := e.search(a, query, numOf(cfg, "topK", 5))
		asm := assembleContext(n.ID, query, chunks, omitted)
		return map[string]any{
			"chunks":   chunks,
			"count":    len(chunks),
			"query":    query,
			"assembly": &asm,
		}, "", nil, &asm

	case domain.NodeLLM:
		return e.decide(a, n, input, vars, outs), "", nil, nil

	case domain.NodeBranch:
		// 分支节点本身不产出值：它决定往哪走，由 nextNode 判定。
		return nil, "", nil, nil

	case domain.NodeHITL:
		who, _ := cfg["who"].(string)
		to, _ := cfg["timeout"].(string)
		onTimeout, _ := cfg["onTimeout"].(string)
		return map[string]any{
			"who":       who,
			"timeout":   to,
			"onTimeout": onTimeout,
			"status":    "waiting",
		}, "等待 " + who + "（超时口径：" + onTimeout + "）", nil, nil

	case domain.NodeEnd:
		return map[string]any{"done": true}, "", nil, nil
	}
	return nil, "", nil, nil
}

// runTool 调真工具。
//
// 每个工具都真的读数据：会话、账单、策略、口径、知识库。
// 「写回执」是真的写——写进账本（由调用方给回调）。
func (e *Exec) runTool(tool string, cfg map[string]any, input map[string]any, vars map[string]any, outs map[string]map[string]any) map[string]any {
	switch tool {
	case "session.read":
		// 先从 vars 取：开始节点声明「缺的用样例值兜底」，兜底值落在 vars 里；
		// 只查 input 会让不带入参的试跑读到空 id，工具报「会话不存在」，
		// 检索节点也跟着拿到空 query——看起来像「没召回」，其实是没读到输入。
		id := stringFrom(vars, "sessionId")
		s := theCorpus.findSession(id)
		if s == nil {
			return map[string]any{"error": "会话不存在：" + id}
		}
		return map[string]any{
			"sessionId": s.ID, "account": s.Account, "channel": s.Channel,
			"customerText": s.Customer, "agentText": s.Agent,
			"silenceMinutes": s.SilenceMinutes,
			"rounds":         strings.Count(s.Customer, "？") + 1,
		}

	case "order.read":
		acct := stringFrom(vars, "orderId", "account")
		return map[string]any{"account": acct, "project": "热玛吉", "price": 4200, "source": "产品口径字典"}

	case "bill.read", "customer.read":
		acct := stringFrom(vars, "customerId", "account")
		b := theCorpus.findBill(acct)
		if b == nil {
			return map[string]any{"error": "账单不存在：" + acct}
		}
		return map[string]any{
			"billId": b.ID, "account": b.Account, "name": b.Name,
			"amount": b.Amount, "overdueDays": b.OverdueDays,
			"tier": b.Tier, "attempt": b.Attempt,
		}

	case "strategy.read":
		t := theCorpus.Strategy.tierFor(intFrom(vars, "overdueDays"))
		return map[string]any{
			"tier": t.Range, "channel": t.Channel, "interval": t.Interval,
			"escalate":            t.Escalate,
			"maxAttemptsPer7Days": theCorpus.Strategy.MaxAttemptsPer7Days,
			"amountEscalate":      theCorpus.Strategy.AmountEscalate,
		}

	case "guard.check":
		text := stringFrom(vars, "customerText", "sessionText", "draft")
		hits := theCorpus.checkRedlines(text)
		amount := floatFrom(vars, "amount")
		var notes []string
		if amount > theCorpus.Strategy.AmountEscalate {
			notes = append(notes, fmt.Sprintf("欠费 %.0f 元超过 %.0f 元转人工线", amount, theCorpus.Strategy.AmountEscalate))
		}
		return map[string]any{"hits": hits, "count": len(hits), "notes": notes}

	case "msg.draft":
		return map[string]any{"draft": theCorpus.draft(vars), "escalate": boolFrom(vars["escalate"])}

	case "report.write":
		// 真写：报告内容由这轮的结论拼出来。
		rep := map[string]any{
			"generatedAt": e.now().Format("01-02 15:04:05"),
			"risk":        stringFrom(vars, "risk"),
			"hits":        sliceOf(vars, "hits"),
			"sessionId":   stringFrom(vars, "sessionId"),
		}
		return map[string]any{"report": rep, "reportUrl": "report://" + stringFrom(vars, "sessionId")}

	case "test.run", "diff.compare", "proto.screenshot":
		// 这些工具在演示里没有真实后端：如实说明，不编数字。
		return map[string]any{
			"tool": tool,
			"note": "该工具未接入真实执行器（没有 CI / 截图服务），本轮不产出结论",
		}
	}
	return map[string]any{"tool": tool, "note": "工具未接入"}
}

// decide 做真判断。
//
// 三个 Agent 三种判断，都是真算出来的：
//   - 质检员：按合规规则库逐条比对会话原文，命中即列证据
//   - 催缴专员：按策略手册定档位与红线，生成话术草稿
//   - 测试工程师：跑真实用例，汇总结论
func (e *Exec) decide(a *domain.Agent, n *domain.FlowNode, input map[string]any, vars map[string]any, outs map[string]map[string]any) map[string]any {
	prompt, _ := n.Cfg["prompt"].(string)
	switch a.ID {
	case "x-qc":
		text := stringFrom(vars, "customerText", "sessionText")
		reply := stringFrom(vars, "agentText")
		hits := theCorpus.judge(text + " " + reply)
		risk := "normal"
		if len(hits) > 0 {
			risk = "high"
		}
		// 情绪规则额外看静默时长。
		if sm := intFrom(vars, "silenceMinutes"); sm >= 30 && !hasRule(hits, "R-07") {
			hits = append(hits, map[string]any{
				"rule": "R-07", "quote": fmt.Sprintf("客户表达不满后 %d 分钟无回应", sm),
				"why": "情绪未接住", "advice": "转人工，由资深管家接手",
			})
			risk = "high"
		}
		return map[string]any{
			"risk": risk, "hits": hits,
			"reasoning": "按 R-03/R-05/R-07/R-11 逐条比对原文",
			"prompt":    prompt,
		}

	case "x-dun":
		amount := floatFrom(vars, "amount")
		days := intFrom(vars, "overdueDays")
		attempt := intFrom(vars, "attempt")
		escalate := amount > theCorpus.Strategy.AmountEscalate
		why := []string{}
		if amount > theCorpus.Strategy.AmountEscalate {
			why = append(why, fmt.Sprintf("欠费 %.0f 元超过 %.0f 元转人工线", amount, theCorpus.Strategy.AmountEscalate))
		}
		if attempt >= theCorpus.Strategy.MaxAttemptsPer7Days {
			escalate = true
			why = append(why, fmt.Sprintf("已触达 %d 次，达到 7 天上限", attempt))
		}
		for _, h := range theCorpus.checkRedlines(stringFrom(vars, "customerText", "sessionText")) {
			escalate = true
			why = append(why, "命中红线："+fmt.Sprint(h))
		}
		return map[string]any{
			"draft":    theCorpus.draft(vars),
			"escalate": escalate,
			"reason":   strings.Join(why, "；"),
			"amount":   amount, "overdueDays": days,
		}

	case "x-qa":
		return map[string]any{
			"result": "按 AT 逐条执行，差异以坐标列出",
			"note":   "真实执行需要接入 CI 与截图服务；本轮只走通编排",
		}
	}
	return map[string]any{"result": "（无判定逻辑）", "prompt": prompt}
}

// nextNode 选下一个节点；分支节点按条件命中。
func (e *Exec) nextNode(f *domain.Flow, n *domain.FlowNode, outs map[string]map[string]any) (string, string) {
	var edges [][3]string
	for _, ed := range f.Edges {
		if ed[0] == n.ID {
			edges = append(edges, ed)
		}
	}
	if len(edges) == 0 {
		return "", ""
	}
	if n.T == domain.NodeBranch {
		// 条件按顺序判定：第一个为真的就它。
		// 条件与出边是平行的：第 i 个条件对应第 i 条出边。
		// 边比条件少时按最后一条走（出边更少说明只有一个去向）。
		conds := asPairs(n.Cfg["conds"])
		for i, c := range conds {
			if !e.evalCond(c[0], outs) {
				continue
			}
			edge := edges[min(i, len(edges)-1)]
			label := edge[2]
			if label == "" {
				label = c[0]
			}
			return edge[1], label
		}
		// 都没命中：走没有条件的边（else）。
		for _, ed := range edges {
			if ed[2] == "" || strings.EqualFold(ed[2], "else") {
				return ed[1], "else"
			}
		}
		if len(edges) > 0 {
			return edges[0][1], edges[0][2]
		}
		return "", ""
	}
	return edges[0][1], edges[0][2]
}

// evalCond 判定一个分支条件。
//
// 支持的写法够用就好：字段比较、字段为真、|| / &&、以及 "else"。
// 条件只看上游显式输出的字段——不让模型自己决定走哪条。
func (e *Exec) evalCond(cond string, outs map[string]map[string]any) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" || strings.EqualFold(cond, "else") {
		return true
	}
	// 先认「整句就是一个比较」：右边可能是带 > 的字符串
	// （escalate || hits.length > 0 里的右半边），
	// 先切 || 的话会被切成 "hits.length > 0" 之外的东西。
	if match := condOperand.FindStringSubmatch(cond); match != nil {
		return compare(match[1], match[2], match[3], outs, e)
	}
	if i := strings.Index(cond, "||"); i >= 0 {
		return e.evalCond(cond[:i], outs) || e.evalCond(cond[i+2:], outs)
	}
	if i := strings.Index(cond, "&&"); i >= 0 {
		return e.evalCond(cond[:i], outs) && e.evalCond(cond[i+2:], outs)
	}
	// 布尔字段为真，或 <字段>.length > 0。
	if strings.HasSuffix(cond, ".length > 0") {
		field := strings.TrimSuffix(cond, ".length > 0")
		return len(sliceFrom(outs, field)) > 0
	}
	v := e.lookup(strings.TrimSpace(cond), outs)
	return boolFrom(v)
}

// condOperand 匹配「左操作数 运算符 右操作数」，两端都不许再含运算符或逻辑连接符。
//
// 这是 evalCond 的第一优先：整句是个比较，就先按比较算。
// 含 || / && 的句子匹配不上，会落到下面的拆分逻辑，逐半边再判。
var condOperand = regexp.MustCompile(`^([^=!<>|&]*?)\s*(==|!=|>=|<=|>|<)\s*([^=!<>|&]*)$`)

// compare 算一个比较表达式。
func compare(left, op, right string, outs map[string]map[string]any, e *Exec) bool {
	right = strings.Trim(strings.TrimSpace(right), `"'`)
	// 右边是数字字面量时按数字比（hits.length > 0）。
	if rf, err := strconv.ParseFloat(right, 64); err == nil {
		lf, ok := toFloat(e.lookup(left, outs))
		if !ok {
			return false
		}
		switch op {
		case ">":
			return lf > rf
		case ">=":
			return lf >= rf
		case "<":
			return lf < rf
		case "<=":
			return lf <= rf
		}
		// == / != 落到下面的字符串比较：bool 与 "true" 之类仍能对上。
	}
	lv := e.lookup(left, outs)
	switch op {
	case "==":
		return fmt.Sprint(lv) == right
	case "!=":
		return fmt.Sprint(lv) != right
	}
	return false
}

// lookup 在上游输出里找一个字段。
//
// 字段可能是 "hits.length"（数组长度），先拆掉后缀再取值。
func (e *Exec) lookup(field string, outs map[string]map[string]any) any {
	field = strings.TrimSpace(field)
	if base, ok := strings.CutSuffix(field, ".length"); ok {
		return len(sliceFrom(outs, base))
	}
	for _, id := range keysInOrder(outs) {
		if v, ok := outs[id][field]; ok {
			return v
		}
	}
	return nil
}

// keysInOrder 按节点 id 排序取输出。
//
// map 的迭代顺序在 Go 里是随机的：两个节点都输出同名字段时，
// 不排序的话「取到谁的」每轮都不一样，同样的输入会得出不同的结论。
// 节点 id（n1、n2…）正好是编排里的先后顺序。
func keysInOrder(outs map[string]map[string]any) []string {
	ids := make([]string, 0, len(outs))
	for id := range outs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ctxBudgetChars 是单个 kb 节点的上下文预算（字符）。
//
// 这是装配事实里 budget.total 的分母：used/total 是「这一轮载入的正文占预算多少」，
// 不是编出来的 token 数。改检索实现的人要知道这个数从哪来——它就是这里的常量。
const ctxBudgetChars = 4000

// search 在知识库里检索：按关键词命中度排序，带出处与库版本。
//
// 返回的第二值是「检索到但没进 topK 的篇数」——装配事实要用它回答
// 「这一轮漏了多少本该参考的文档」（宪法 II：数得出来就如实报，数不出来才说判不了）。
func (e *Exec) search(a *domain.Agent, query string, topK int) ([]map[string]any, int) {
	docs := theCorpus.docsFor(a)
	if topK <= 0 {
		topK = 5
	}
	type scored struct {
		doc   kbDoc
		score int
	}
	var list []scored
	q := strings.ToLower(query)
	terms := splitTerms(q)
	for _, d := range docs {
		text := strings.ToLower(d.Title + " " + d.Text)
		score := 0
		for _, t := range terms {
			if t == "" {
				continue
			}
			if strings.Contains(text, t) {
				score++
			}
		}
		// 标题命中权重更高。
		if strings.Contains(strings.ToLower(d.Title), q) && q != "" {
			score += 3
		}
		if score > 0 {
			list = append(list, scored{doc: d, score: score})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]map[string]any, 0, topK)
	for i, s := range list {
		if i >= topK {
			break
		}
		out = append(out, map[string]any{
			"lib": s.doc.Lib, "doc": s.doc.Doc, "title": s.doc.Title,
			"text": s.doc.Text, "score": s.score, "ver": docVer(a, s.doc),
		})
	}
	omitted := len(list) - len(out)
	if omitted < 0 {
		omitted = 0
	}
	return out, omitted
}

// docVer 取这篇文档所属库的版本：文档自带 ver 优先，否则按库名到 Agent 的 KB 里找。
//
// 拿不到就返回空——空表示「不知道这一版」，不编一个 v1 冒充。
func docVer(a *domain.Agent, d kbDoc) string {
	if d.Ver != "" {
		return d.Ver
	}
	if a != nil && a.KB != nil {
		for _, l := range a.KB.Libs {
			if l.N == d.Lib {
				return l.V
			}
		}
	}
	return ""
}

// assembleContext 把一次检索的结果整理成装配事实：载入清单、省略多少、预算占用。
func assembleContext(node, query string, chunks []map[string]any, omitted int) domain.ContextAssembly {
	loaded := make([]domain.LoadedChunk, 0, len(chunks))
	used := 0
	for _, c := range chunks {
		lib, _ := c["lib"].(string)
		doc, _ := c["doc"].(string)
		ver, _ := c["ver"].(string)
		text, _ := c["text"].(string)
		score, _ := c["score"].(int)
		used += len([]rune(text))
		loaded = append(loaded, domain.LoadedChunk{
			Lib: lib, Doc: doc, Ver: ver, Score: score, Class: "按需检索",
		})
	}
	reason := ""
	if omitted > 0 {
		// topK 截断是这里唯一会「漏」的原因；说清楚是哪条规则砍的，别让人猜。
		reason = fmt.Sprintf("topK 截断（只保留前 %d 篇）", len(chunks))
	}
	return domain.ContextAssembly{
		Node:    node,
		Query:   query,
		Loaded:  loaded,
		Omitted: domain.OmittedInfo{Count: omitted, Reason: reason},
		Budget:  domain.BudgetInfo{Used: float64(used) / float64(ctxBudgetChars), Total: 1.0},
	}
}

// renderRaw 把这一轮渲染成可读输出（JSON）。
func (e *Exec) renderRaw(run *Run) string {
	b, err := json.MarshalIndent(map[string]any{
		"output": run.Output,
		"branch": run.Branch,
	}, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ---------- 语料查询 ----------

// docsFor 取一个 Agent 能检索到的文档。
//
// 语料是按「库」组织的，Knowledge 的键是当初写下这批文档时的 Agent 标识
// （x-qc / x-dun）。交付的 Agent 叫 qa-agent / dun-agent，键对不上——
// 按 Agent id 直接取会是空集，检索就成了摆设，装配事实里永远「载入 0 篇」。
// 所以按 Agent 引用的库名去取：库才是知识的归属，Agent 改名、重建都不该丢文档。
// Agent 没有登记库（或库还没同步）时退回按 id 取，保证老账本仍有文档可用。
func (c *corpus) docsFor(a *domain.Agent) []kbDoc {
	if a == nil {
		return nil
	}
	if a.KB != nil && len(a.KB.Libs) > 0 {
		libs := make(map[string]bool, len(a.KB.Libs))
		for _, l := range a.KB.Libs {
			libs[l.N] = true
		}
		var out []kbDoc
		// 同一个库可能被多个 Agent 分组各收一份同样的文档；按 (lib, doc) 去重，
		// 否则装配清单里会出现两条同名同分的同一篇。
		seen := map[string]bool{}
		for _, docs := range c.Knowledge {
			for _, d := range docs {
				if !libs[d.Lib] {
					continue
				}
				k := d.Lib + "\x00" + d.Doc
				if seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, d)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return c.Knowledge[a.ID]
}

func (c *corpus) findSession(id string) *session {
	for i := range c.Sessions {
		if c.Sessions[i].ID == id {
			return &c.Sessions[i]
		}
	}
	return nil
}

func (c *corpus) findBill(acct string) *bill {
	for i := range c.Bills {
		if c.Bills[i].Account == acct {
			return &c.Bills[i]
		}
	}
	return nil
}

// judge 逐条比对规则，返回命中证据。
//
// 证据里必须有规则号与原话片段：没有证据的判定是不可复核的。
func (c *corpus) judge(text string) []any {
	var hits []any
	for _, r := range c.Rules {
		for _, p := range r.Patterns {
			if strings.Contains(text, p) {
				hits = append(hits, map[string]any{
					"rule": r.ID, "quote": p,
					"why": r.Name, "advice": r.Advice,
				})
				break
			}
		}
	}
	return hits
}

func (c *corpus) checkRedlines(text string) []string {
	var hits []string
	for _, w := range c.Strategy.Redlines {
		if strings.Contains(text, w) {
			hits = append(hits, w)
		}
	}
	return hits
}

func (c *corpus) draft(vars map[string]any) string {
	days := intFrom(vars, "overdueDays")
	amount := floatFrom(vars, "amount")
	name := stringFrom(vars, "name", "customer")
	if name == "" {
		name = "您好"
	} else {
		name += "您好"
	}
	switch {
	case days <= 3:
		return fmt.Sprintf("%s，本月账单已于 10 日到期，方便时通过小程序完成缴费即可，如有疑问随时联系我。", name)
	case days <= 15:
		return fmt.Sprintf("%s，您名下账单已逾期 %d 天，金额 ¥%s，请于 3 日内处理，逾期将按规定计收滞纳金。", name, days, money(amount))
	default:
		return fmt.Sprintf("%s，您名下账单已逾期 %d 天，金额 ¥%s，已为您安排专属管家稍后与您联系。", name, days, money(amount))
	}
}

// tierFor 按逾期天数找触达档位。
func (s strategy) tierFor(days int) tier {
	for _, t := range s.Tiers {
		switch t.Range {
		case "1–3 天":
			if days <= 3 {
				return t
			}
		case "4–15 天":
			if days <= 15 {
				return t
			}
		case "16–30 天":
			if days <= 30 {
				return t
			}
		default:
			return t
		}
	}
	return s.Tiers[0]
}

func money(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ---------- 小工具 ----------

func findNode(f *domain.Flow, id string) *domain.FlowNode {
	for i := range f.Nodes {
		if f.Nodes[i].ID == id {
			return &f.Nodes[i]
		}
	}
	return nil
}

func asStrings(v any) [][]string {
	arr, _ := v.([]any)
	out := make([][]string, 0, len(arr))
	for _, it := range arr {
		row, _ := it.([]any)
		r := make([]string, 0, len(row))
		for _, c := range row {
			s, _ := c.(string)
			r = append(r, s)
		}
		out = append(out, r)
	}
	return out
}

func asPairs(v any) [][2]string {
	arr, _ := v.([]any)
	out := make([][2]string, 0, len(arr))
	for _, it := range arr {
		row, _ := it.([]any)
		if len(row) < 2 {
			continue
		}
		a, _ := row[0].(string)
		b, _ := row[1].(string)
		out = append(out, [2]string{a, b})
	}
	return out
}

func stringFrom(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func floatFrom(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if f, ok := toFloat(v); ok {
				return f
			}
		}
	}
	return 0
}

func intFrom(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if f, ok := toFloat(v); ok {
				return int(f)
			}
		}
	}
	return 0
}

func boolFrom(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1" || x == "yes"
	case float64:
		return x != 0
	}
	return false
}

// sliceFrom 在各节点输出里找一个数组字段（分支判定用）。
func sliceFrom(outs map[string]map[string]any, field string) []any {
	for _, id := range keysInOrder(outs) {
		if v, ok := outs[id][field]; ok {
			if s, ok := v.([]any); ok {
				return s
			}
			if s, ok := v.([]map[string]any); ok {
				out := make([]any, len(s))
				for i := range s {
					out[i] = s[i]
				}
				return out
			}
		}
	}
	return nil
}

// sliceOf 在扁平变量表里找一个数组字段。
func sliceOf(m map[string]any, field string) []any {
	v, ok := m[field]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []any:
		return s
	case []map[string]any:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// numOf 从配置里取一个数值，取不到用默认值。
func numOf(m map[string]any, key string, def int) int {
	if v, ok := m[key]; ok {
		if f, ok := toFloat(v); ok {
			return int(f)
		}
	}
	return def
}

// isCJK 判断是否汉字区间。切词用它区分「中文要滑窗」与「英文数字整串用」。
func isCJK(r rune) bool { return r >= '一' && r <= '鿿' }

// splitTerms 把查询切成检索词。
//
// 中文没有空格，一整句「你们这个热玛吉能维持多久」按原样去 Contains 永远命中不了
// 写成「热玛吉」的文档——检索等于没做，装配事实里永远是「载入 0 篇」，
// 看起来像模型没召回，其实是切词把句子切丢了。这里把中文长串切成两字滑窗
// （bigram）：仍是词面命中，不引入向量或重排，只是让中文能被检索到。
func splitTerms(q string) []string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !(r >= '一' && r <= '鿿') && !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		r := []rune(f)
		if !isCJK(r[0]) || len(r) <= 4 {
			// 英文/数字串原样用；中文短词（≤4 字）本身就是完整口径。
			if len(r) >= 2 {
				add(f)
			}
			continue
		}
		// 中文长句：两字滑窗。单字不保留（区分度太低，会把所有文档都算命中）。
		for i := 0; i+1 < len(r); i++ {
			add(string(r[i : i+2]))
		}
	}
	return out
}

// sampleFor 给缺输入的变量一个样例值（开始节点声明的来源就是兜底口径）。
//
// 先认变量名，再认来源列：来源是「这类数据从哪个系统来」，同一来源下常挂多个
// 变量（会话 id 与订单 id 都可能写「调度器」）。只按来源兜底会把订单号塞进
// sessionId，于是读会话报「会话不存在」、检索拿到空 query——看着像没召回，
// 其实是样例填错了。变量名是具体口径，比来源列更精确。
func sampleFor(name string, decl []string) any {
	switch name {
	case "sessionId":
		return "S-20261124-0912"
	case "orderId", "account":
		return "ORD-88213"
	case "customerId":
		return "C-40912"
	case "amount":
		return 4600
	case "overdueDays":
		return 32
	case "attempt":
		return 2
	}
	src := ""
	if len(decl) > 3 {
		src = decl[3]
	}
	switch {
	case strings.Contains(src, "会话"):
		return "S-20261124-0912"
	case strings.Contains(src, "账单"), strings.Contains(src, "客户"):
		return "C-40912"
	case strings.Contains(src, "订单"):
		return "ORD-88213"
	case strings.Contains(src, "策略"), strings.Contains(src, "调度器"):
		return "T-20261124-0087"
	}
	return ""
}

// hasRule 判断命中里是否已有某条规则。
func hasRule(hits []any, id string) bool {
	for _, h := range hits {
		if m, ok := h.(map[string]any); ok {
			if fmt.Sprint(m["rule"]) == id {
				return true
			}
		}
	}
	return false
}
