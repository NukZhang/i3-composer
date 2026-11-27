package bff

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

// 本文件是「按 tag 回放」的只读出口。
//
// 宪法 I：状态只能由 append-only 事实重放得出。账本一直是逐条哈希链，
// 但重放过去只有一个可选时间点：现在。这个接口把时间点变成参数——
// 「这个 tag 打下去的那一刻，账本是什么样、闸为什么全绿」能当场重算出来。
//
// 它不是视频回放，也不是第三套真相：就是 Project(事实窗口) 的输出。
// 窗口的右端是 tag.created 的 seq，包含它自己。

// replayFact 是回放窗口里的一条事实，只给查询用的标量。
type replayFact struct {
	Seq   int    `json:"seq"`
	At    string `json:"at"`
	Type  string `json:"type"`
	Actor string `json:"actor"`
}

// replayView 是回放结果。
type replayView struct {
	Unit    string          `json:"unit"`
	Tag     string          `json:"tag"`
	Commit  string          `json:"commit"`
	Seq     int             `json:"seq"` // tag.created 的 seq：窗口右端
	At      string          `json:"at"`
	Tags    []string        `json:"tags"` // 打到这个 tag 为止的全部 tag
	Commits []domain.Commit `json:"commits"`
	Gates   []domain.Gate   `json:"gates"`
	// GatesNote 在「当时的闸没有落账」时说明原因，而不是补一组今天的闸。
	// 判不了就说判不了，别把现在当当时。
	GatesNote string            `json:"gatesNote,omitempty"`
	Checks    []domain.Check    `json:"checks"`
	Test      *store.TestResult `json:"test,omitempty"`
	Facts     []replayFact      `json:"facts"`
}

// replay 是 GET /api/units/{id}/replay?tag=vX。
//
// 已打 tag 的单元返回 200；单元不存在 404；单元存在但没打过 tag、
// 或指定的 tag 不存在，返回 400 并说清是哪种、有哪些 tag 可选。
func (s *Server) replay(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	id := r.PathValue("id")
	uv, err := s.loadUnits(r.Context())
	if err != nil {
		return err
	}
	var u store.Unit
	found := false
	for _, x := range uv.Units {
		if x.ID == id {
			u, found = x, true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w：单元 %s 不存在", errNotFound, id)
	}

	facts, err := s.Log.Load(r.Context(), id)
	if err != nil {
		return err
	}
	// 账本顺序即 seq 顺序：Load 已按 seq 升序返回，这里不再排序。
	var tags []string
	var taggedAt int // tag.created 的 seq；0 = 还没打过 tag
	var at string
	var commitNo string
	want := strings.TrimSpace(r.URL.Query().Get("tag"))
	for _, f := range facts {
		if f.Type != store.FactTagCreated {
			continue
		}
		var p struct {
			Tag    string `json:"tag"`
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			return fmt.Errorf("重放 tag.created 失败：%w", err)
		}
		if p.Tag == "" {
			continue
		}
		tags = append(tags, p.Tag)
		// 不指定 tag 时取最近一次——「最近一次交付」是最常问的那个时点。
		if want == "" || p.Tag == want {
			taggedAt, at, commitNo = f.Seq, f.At, p.Commit
		}
	}
	if want == "" && taggedAt == 0 {
		return fmt.Errorf("%w：单元 %s 还没打过 tag，没有可回放的交付点", errBadRequest, u.Name)
	}
	if want != "" && taggedAt == 0 {
		if len(tags) == 0 {
			return fmt.Errorf("%w：单元 %s 还没打过 tag，没有可回放的交付点", errBadRequest, u.Name)
		}
		return fmt.Errorf("%w：单元 %s 没有 tag %s（可选：%s）", errBadRequest, u.Name, want, strings.Join(tags, "、"))
	}

	// 窗口含 tag.created 本身：tag 是这一刻发生的，不是它之后才发生。
	v, err := store.ProjectAsOf(id, facts, taggedAt)
	if err != nil {
		return err
	}

	out := replayView{
		Unit: id, Tag: tagOf(tags, want), Commit: commitNo, Seq: taggedAt, At: at,
		Tags: v.Tags, Commits: v.Commits, Checks: v.Checks, Test: v.Test,
	}
	// 空切片而不是 null：前端直接读 .length / .filter，null 会把它变成白屏。
	if out.Checks == nil {
		out.Checks = []domain.Check{}
	}
	if out.Commits == nil {
		out.Commits = []domain.Commit{}
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	for _, f := range store.UpTo(facts, taggedAt) {
		out.Facts = append(out.Facts, replayFact{Seq: f.Seq, At: f.At, Type: f.Type, Actor: f.Actor})
	}
	// 旧账本可能留有打 tag 失败时的判定，优先读最接近该 tag 的一组。
	for i := len(v.GateEvals) - 1; i >= 0; i-- {
		e := v.GateEvals[i]
		if e.Tag == out.Tag && (out.Commit == "" || e.Commit == "" || e.Commit == out.Commit) {
			out.Gates = e.Gates
			break
		}
	}
	if out.Gates == nil {
		out.GatesNote = "这个 tag 打的时候没有记录闸门（gate.evaluated 从本轮起才落账）；要看今天的闸请读 /api/state，那算的是现在，不是当时"
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// tagOf 返回实际回放的 tag。指定了就按指定的（上面已校验存在），
// 没指定就是最近一次。
func tagOf(tags []string, want string) string {
	if want != "" {
		return want
	}
	if len(tags) == 0 {
		return ""
	}
	return tags[len(tags)-1]
}
