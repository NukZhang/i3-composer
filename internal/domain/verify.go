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
type Check struct {
	ID string `json:"id"`
	N  string `json:"n"`
	G  string `json:"g"`
	W  string `json:"w"`
	T  string `json:"t"`
	ST string `json:"st"` // pass | fail | todo
}

// 验收结论。只有两种；「不提过」必须带一条 bug，否则没有下文。
const (
	CheckPass = "pass"
	CheckFail = "fail"
	CheckTodo = "todo"
)

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
