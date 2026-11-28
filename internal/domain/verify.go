package domain

import (
	"fmt"
	"regexp"
	"strconv"
)

// 本文件是原型 v2.5/v2.6 的「真人验收台」领域模型。
//
// 验证不是看报告，是人自己上手跑一遍：照着清单操作、判过不过，
// 不过就提 bug。机器自检（四闸 / 评测台 / 知识库准确度）属于构建期的
// 「质量报告」，不算验收。

// 验收清单的一条 AC。G/W/T 是 Given / When / Then。
//
// Commit 记这条 AC 出自哪个提交的 AT：清单跟提交走，不是跟单元走。
// 没有它，「验过了」就说不清验的是哪一版——改完代码再打 tag 也能蒙混过去。
type Check struct {
	ID     string `json:"id"`
	N      string `json:"n"`
	G      string `json:"g"`
	W      string `json:"w"`
	T      string `json:"t"`
	ST     string `json:"st"`               // pass | fail | todo
	Commit string `json:"commit,omitempty"` // 这条 AC 出自哪个提交
}

// 验收结论。只有两种；「不提过」必须带一条 bug，否则没有下文。
const (
	CheckPass = "pass"
	CheckFail = "fail"
	CheckTodo = "todo"
)

// CheckBlock 说明验收清单凭什么挡住打 tag。返回空串表示放行。
//
// 清单必须「有、是这一版的、全验过」三件齐了才算数：
//   - 没有清单：新拆出来的单元还没出过 AT，机器测试过了也不代表人验过；
//   - 出自别的提交：清单跟着旧版本走，验的不是这一版；
//   - 还有 todo / fail：没验完或验不过。
//
// 不认识的结论（空串、拼错的状态）按「还没验」算——宁可挡住，也不把
// 一条看不懂的记录当成通过。
func CheckBlock(list []Check, head string) string {
	if len(list) == 0 {
		return "没有验收清单：本单元还没出过 AT，清单随 AT 制品的 AC 进账本"
	}
	var todo, fail int
	for _, c := range list {
		switch c.ST {
		case CheckPass:
		case CheckFail:
			fail++
		default:
			todo++
		}
	}
	// 「还没验」先报：它比「验的是旧版」更直接地说明该做什么。
	if todo+fail > 0 {
		return fmt.Sprintf("还差 %d 条验收未通过（%d/%d）", todo+fail, len(list)-(todo+fail), len(list))
	}
	// 全验过，但验的是旧提交：典型情况是打 tag 前又改了一版代码。
	for _, c := range list {
		if c.Commit != head {
			return fmt.Sprintf("验收清单出自提交 %s，不是 HEAD %s：改完代码要重出 AT 再验", c.Commit, head)
		}
	}
	return ""
}

// CheckValid 判定验收结论是否合法。
func CheckValid(st string) bool {
	return st == CheckPass || st == CheckFail || st == CheckTodo
}

// DecideCheck 返回判定后的新清单。纯函数：调用方负责把结论写进账本。
func DecideCheck(list []Check, id, st string) ([]Check, error) {
	if !CheckValid(st) {
		return nil, fmt.Errorf("验收结论只能是 pass / fail")
	}
	for i := range list {
		if list[i].ID == id {
			out := append([]Check(nil), list...)
			out[i].ST = st
			return out, nil
		}
	}
	return nil, fmt.Errorf("验收项 %s 不存在", id)
}

// Defect 是验收不通过时提出的 bug。它进 backlog，由 X 修；
// 修完这一条重验，其它条不受影响。
type Defect struct {
	ID    string `json:"id"`
	PBC   string `json:"pbc,omitempty"`
	AC    string `json:"ac"`
	T     string `json:"t"`
	Sev   string `json:"sev"`
	Steps string `json:"steps,omitempty"`
	At    string `json:"at"`
	By    string `json:"by"`
}

// Sevs 是原型里的严重级选项。
var Sevs = []string{"阻塞", "严重", "一般", "建议"}

// SevValid 判定严重级是否合法。
func SevValid(s string) bool {
	for _, x := range Sevs {
		if s == x {
			return true
		}
	}
	return false
}

var defectRe = regexp.MustCompile(`^BUG-(\d+)$`)

// NextDefectID 从现有 bug 继续编号。从 BUG-118 起算，和原型种子一致。
func NextDefectID(defects []Defect) string {
	max := 117
	for _, d := range defects {
		if m := defectRe.FindStringSubmatch(d.ID); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("BUG-%d", max+1)
}

// AcceptanceStats 是清单的进度：已验 / 通过 / 不过。
type AcceptanceStats struct {
	Done  int `json:"done"`
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Total int `json:"total"`
}

func Stats(list []Check) AcceptanceStats {
	s := AcceptanceStats{Total: len(list)}
	for _, c := range list {
		switch c.ST {
		case CheckPass:
			s.Done++
			s.Pass++
		case CheckFail:
			s.Done++
			s.Fail++
		}
	}
	return s
}
