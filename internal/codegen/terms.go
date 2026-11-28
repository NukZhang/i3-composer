package codegen

// 术语：按工作项标题给本单元的业务名词起名（#93）。
//
// 以前生成的代码里写死了「滞纳金 0.05% / 封顶 20%」「账单」「A-101」——
// 那是模板作者拍板的业务口径，不是这条工作项的规格。标题「批量生成缴费单」
// 生成的却是账单滞纳金，等于把没确认过的业务决策塞进交付物。
//
// 这里只做一件事：把标题里出现的业务名词认出来，代码、测试、AT 都用同一份。
// 认不出来就用标题本身——宁可生一个「通用记录」，也不套一个别的业务的口径。
// 规则是确定性的、无状态：同样的标题永远同一份术语，digest 才可复算。

import (
	"strings"
	"unicode/utf8"
)

// Term 是一条业务术语。
type Term struct {
	Word   string    // 术语本身（「缴费单」「工单」）
	Plural string    // 资源集合名，URL 路径用（「invoices」）
	Fields [3]string // 这一行记录里最要紧的三个可读字段（小写下划线，JSON 名）
	Shown  [3]string // 字段的中文说明
	Amount bool      // 是否带「金额」字段（统计接口的金额合计要有意义）
}

// 几条已知的术语。顺序即优先级：标题里先出现谁就用谁。
var knownTerms = []struct {
	words  []string
	plural string
	fields [3]string
	shown  [3]string
	amount bool
}{
	{[]string{"缴费单", "缴费", "账单", "欠费"}, "invoices",
		[3]string{"account", "due_date", "note"}, [3]string{"户号", "应缴日期", "备注"}, true},
	{[]string{"工单", "报修", "报障"}, "tickets",
		[3]string{"customer", "priority", "summary"}, [3]string{"客户", "优先级", "摘要"}, false},
	{[]string{"巡检"}, "inspections",
		[3]string{"site", "result", "inspected_at"}, [3]string{"点位", "结果", "巡检时间"}, false},
	{[]string{"任务", "待办"}, "tasks",
		[3]string{"owner", "due_date", "summary"}, [3]string{"负责人", "截止日", "摘要"}, false},
}

// generic 是认不出来时用的通用记录：不假设任何业务口径。
var generic = struct {
	plural string
	fields [3]string
	shown  [3]string
}{"records", [3]string{"title", "status", "created_at"}, [3]string{"标题", "状态", "创建时间"}}

// Terms 从工作项标题推出本单元的术语。
func (s Spec) Terms() Term {
	title := s.Title
	best, at := -1, len(title)+1
	for i, k := range knownTerms {
		for _, w := range k.words {
			if idx := strings.Index(title, w); idx >= 0 && idx < at {
				best, at = i, idx
			}
		}
	}
	if best >= 0 {
		k := knownTerms[best]
		return Term{Word: k.words[0], Plural: k.plural, Fields: k.fields, Shown: k.shown, Amount: k.amount}
	}
	// 标题的第一个词（按标点与空格切）当资源名；太短或太长就退回通用记录。
	head := firstNoun(title)
	if head == "" || utf8.RuneCountInString(head) > 8 {
		head = "记录"
	}
	return Term{Word: head, Plural: pinyinFallback(s.UnitID), Fields: generic.fields, Shown: generic.shown}
}

// firstNoun 取标题里第一个足够长的词，剥掉「批量生成」「支持」这类动词前缀。
func firstNoun(title string) string {
	cut := strings.NewReplacer("，", " ", ",", " ", "。", " ", "、", " ", "：", " ", ":", " ").Replace(title)
	for _, w := range strings.Fields(cut) {
		w = strings.TrimSpace(w)
		for _, p := range []string{"批量生成", "批量", "生成", "支持", "新增", "增加", "修改", "优化", "修复"} {
			w = strings.TrimPrefix(w, p)
		}
		if utf8.RuneCountInString(w) >= 2 {
			return w
		}
	}
	return ""
}

// pinyinFallback 没有英文资源名可用时，用单元 id 拼一个合法路径段。
// 中文标题没法直接进 URL，这里只保证合法与稳定，不追求可读。
func pinyinFallback(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return generic.plural
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return generic.plural
	}
	return b.String() + "s"
}

// pluralOf 保留旧的按单元 id 取集合名的行为，给不关心标题的调用用。
func (s Spec) pluralOf() string {
	switch s.UnitID {
	case "fee":
		return "bills"
	case "qa":
		return "reviews"
	case "dun":
		return "collections"
	default:
		return pinyinFallback(s.UnitID)
	}
}

// sampleValue 是某条记录里一个可读字段的示例值：字段本身没有业务取值，
// 生成时只给确定性的占位（不编业务数据）。
func sampleValue(field, key string) string {
	switch field {
	case "account":
		return key
	case "amount", "due_date", "created_at", "inspected_at":
		return "{{AT}}"
	}
	return field + ":" + key
}
