package bff

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// admitInput 把账本投影翻译成准入的判据。
//
// 这一层刻意薄：domain.Admit 不碰 IO，能不能判对，全看这里把哪些事实
// 递了进去。所以每条判据都要指明来源——
//
//	验收口径 ← 工作项自己写的 accept[]；没有才退到单元里「手动编辑」的 AT / PRD
//	数据范围 ← 工作项自己写的 data[]
//	责任人   ← 工作项自己写的 acceptBy / complyBy
//	范围边界 ← 有没有 task 挂在它下面
//	上下文   ← parent，或单元有没有 Backlog 制品
//
// 机器生成的 PRD / AT 不算人的验收口径：它们的正文是 codegen 照着标题套的模板，
// 拿它当门槛证据，等于「准入自己给自己发合格证」。
func (s *Server) admitInput(v *store.View, it *store.Item) domain.AdmitInput {
	in := domain.AdmitInput{
		ID: it.ID, Title: it.Title, Type: it.Type, Kind: it.Kind, Parent: it.Parent,
		Accept: it.Accept, Data: it.Data,
		AcceptBy: it.AcceptBy, ComplyBy: it.ComplyBy,
	}

	// 有没有拆到 task：这个单元的 items 在 v.Items 里。
	for _, x := range v.Items {
		if x.Parent == it.ID {
			in.HasTaskBreakdown = true
			break
		}
	}
	in.HasBacklog = domain.Latest(v.Versions, domain.Backlog) != ""

	// 人写下的验收口径：AT 优先，其次 PRD；只认 source=手动编辑 的最新一版。
	if md, ok := manualText(v.Versions, domain.AT); ok {
		in.HumanAcceptText, in.HumanAcceptFrom = md, "AT"
	} else if md, ok := manualText(v.Versions, domain.PRD); ok {
		in.HumanAcceptText, in.HumanAcceptFrom = md, "PRD"
	}

	// AD 只用来核「声明的表与设计对不对得上」；哪一版都行，最新的最接近现状。
	if vs := v.Versions[domain.AD]; len(vs) > 0 {
		in.ADText = strings.TrimSpace(vs[len(vs)-1].MD)
	}
	return in
}

// manualText 取指定制品「人写下的」最新一版正文。
//
// 判据是 Source == "手动编辑"（见 v23.go saveDraft）。生成与重算的版本
// 都是模板产物，不能拿来证明人的意图。
func manualText(versions map[domain.Artifact][]domain.Version, t domain.Artifact) (string, bool) {
	vs := versions[t]
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].Source == "手动编辑" {
			if md := strings.TrimSpace(vs[i].MD); md != "" {
				return md, true
			}
		}
	}
	return "", false
}

// admitInputs 是 POST /api/items/{id}/inputs：补准入输入。
//
// 准入拒绝之后，账本必须留一条「补哪」可走的路，否则提示是空的。
// 能补的只有三项硬项的输入（验收口径 / 数据范围 / 责任人）——
// 范围边界和上下文齐备不在这里补，它们靠 project 的拆解与制品，
// 补一个字段并不能让它们成真。
//
// 权限挂在 ActBacklog（BA）：这些是需求本身的内容，不是交付动作。
func (s *Server) admitInputs(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActBacklog); err != nil {
		return err
	}
	var in struct {
		Accept   []string `json:"accept"`
		Data     []string `json:"data"`
		AcceptBy string   `json:"acceptBy"`
		ComplyBy string   `json:"complyBy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		return fmt.Errorf("%w：%v", errBadRequest, err)
	}
	in.AcceptBy = strings.TrimSpace(in.AcceptBy)
	in.ComplyBy = strings.TrimSpace(in.ComplyBy)
	if len(cleanInputs(in.Accept)) == 0 && len(cleanInputs(in.Data)) == 0 &&
		in.AcceptBy == "" && in.ComplyBy == "" {
		return fmt.Errorf("%w：没给任何要补的输入（accept / data / acceptBy / complyBy）", errBadRequest)
	}
	v, err := s.load(r, c.PBC)
	if err != nil {
		return err
	}
	if _, ok := v.Items[c.ItemID]; !ok {
		return fmt.Errorf("%w：%s", errNotFound, c.ItemID)
	}
	// 补的是输入，不是结论：这里只追加一条 item.admit.inputs，
	// 不改写历史上已经落下的 item.admitted / item.admit.rejected。
	// 下一次发起按最新输入重判，上一轮的结论在账本里仍然查得到。
	if _, err := s.Log.Append(r.Context(), c.PBC, store.FactItemInputs, c.Role.String(),
		map[string]any{
			"id": c.ItemID, "accept": cleanInputs(in.Accept), "data": cleanInputs(in.Data),
			"acceptBy": in.AcceptBy, "complyBy": in.ComplyBy, "by": c.Role.String(),
		}); err != nil {
		return err
	}
	s.publish(c.PBC, "item.admit.inputs", map[string]any{"id": c.ItemID})
	writeJSON(w, http.StatusOK, map[string]any{"id": c.ItemID})
	return nil
}

// cleanInputs 去掉空白项并去重（保序）。同一张表写两遍不是两项声明。
func cleanInputs(xs []string) []string {
	out := make([]string, 0, len(xs))
	seen := map[string]bool{}
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
