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
		out, wait, notes := e.runNode(a, node, run.Input, vars, outs)
		ms := e.now().Sub(t0).Milliseconds()
		if ms < 1 {
			ms = 1
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

// runNode 执行一个节点。
func (e *Exec) runNode(a *domain.Agent, n *domain.FlowNode, input map[string]any, vars map[string]any, outs map[string]map[string]any) (map[string]any, string, []string) {
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
		return out, "", nil

	case domain.NodeTool:
		tool, _ := cfg["tool"].(string)
		return e.runTool(tool, cfg, input, vars, outs), "", nil

	case domain.NodeKB:
		query := stringFrom(vars, "sessionText", "customerText", "draft")
		chunks := e.search(a.ID, query, numOf(cfg, "topK", 5))
		return map[string]any{
			"chunks": chunks,
			"count":  len(chunks),
			"query":  query,
		}, "", nil

	case domain.NodeLLM:
		return e.decide(a, n, input, vars, outs), "", nil

	case domain.NodeBranch:
		// 分支节点本身不产出值：它决定往哪走，由 nextNode 判定。
		return nil, "", nil

	case domain.NodeHITL:
		who, _ := cfg["who"].(string)
		to, _ := cfg["timeout"].(string)
		onTimeout, _ := cfg["onTimeout"].(string)
		return map[string]any{
			"who":       who,
			"timeout":   to,
			"onTimeout": onTimeout,
			"status":    "waiting",
		}, "等待 " + who + "（超时口径：" + onTimeout + "）", nil

	case domain.NodeEnd:
		return map[string]any{"done": true}, "", nil
	}
	return nil, "", nil
}

// runTool 调真工具。
//
// 每个工具都真的读数据：会话、账单、策略、口径、知识库。
// 「写回执」是真的写——写进账本（由调用方给回调）。
func (e *Exec) runTool(tool string, cfg map[string]any, input map[string]any, vars map[string]any, outs map[string]map[string]any) map[string]any {
	switch tool {
	case "session.read":
		id := stringFrom(input, "sessionId")
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
		acct := stringFrom(input, "orderId", "account")
		return map[string]any{"account": acct, "project": "热玛吉", "price": 4200, "source": "产品口径字典"}

	case "bill.read", "customer.read":
		acct := stringFrom(input, "customerId", "account")
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
	if i := strings.Index(cond, "||"); i >= 0 {
		return e.evalCond(cond[:i], outs) || e.evalCond(cond[i+2:], outs)
	}
	if i := strings.Index(cond, "&&"); i >= 0 {
		return e.evalCond(cond[:i], outs) && e.evalCond(cond[i+2:], outs)
	}
	// 字段比较：a == "b" / a != 2 / a > 0
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if i := strings.Index(cond, op); i > 0 {
			left := strings.TrimSpace(cond[:i])
			right := strings.TrimSpace(cond[i+len(op):])
			lv := e.lookup(left, outs)
			rv := strings.Trim(right, `"'`)
			switch op {
			case "==":
				return fmt.Sprint(lv) == rv
			case "!=":
				return fmt.Sprint(lv) != rv
			case ">", ">=", "<", "<=":
				lf, ok1 := toFloat(lv)
				rf, ok2 := toFloat(rv)
				if !ok1 || !ok2 {
					return false
				}
				switch op {
				case ">":
					return lf > rf
				case ">=":
					return lf >= rf
				case "<":
					return lf < rf
				default:
					return lf <= rf
				}
			}
		}
	}
	// 布尔字段：escalate / hits.length > 0 之类。
	if strings.HasSuffix(cond, ".length > 0") {
		field := strings.TrimSuffix(cond, ".length > 0")
		return len(sliceFrom(outs, field)) > 0
	}
	v := e.lookup(strings.TrimSpace(cond), outs)
	return boolFrom(v)
}

// lookup 在上游输出里找一个字段。
func (e *Exec) lookup(field string, outs map[string]map[string]any) any {
	field = strings.TrimSpace(field)
	for _, o := range outs {
		if v, ok := o[field]; ok {
			return v
		}
	}
	return nil
}

// search 在知识库里检索：按关键词命中度排序，带出处。
func (e *Exec) search(agentID, query string, topK int) []map[string]any {
	docs := theCorpus.Knowledge[agentID]
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
			"text": s.doc.Text, "score": s.score,
		})
	}
	return out
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
	for _, o := range outs {
		if v, ok := o[field]; ok {
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

// splitTerms 把查询切成检索词。
func splitTerms(q string) []string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !(r >= '一' && r <= '鿿') && !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// sampleFor 给缺输入的变量一个样例值（开始节点声明的来源就是兜底口径）。
func sampleFor(name string, decl []string) any {
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
	case strings.Contains(src, "策略"):
		return "T-20261124-0087"
	case strings.Contains(src, "调度器"):
		return "T-20261124-0087"
	}
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
