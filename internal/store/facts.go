// Package store 实现事实日志：append-only、逐条哈希链、head 用 etag CAS 串行写。
//
// 这是整条链路里唯一的「权威」。Workflow history 不是权威：
// 它属于单个实例、可被 purge、随引擎版本变化，所以它只负责编排，
// 状态一律以本日志为准，投影任何时候都能从日志重放出来。
//
// 存储键的设计有个要点：条目键是 log:{pbc}:{seq}:{hash}，而不是 log:{pbc}:{seq}。
// 两个写入者并发追加同一条 seq 时，它们的内容不同 → 哈希不同 → 落到不同键上，
// 只有 CAS 赢家的那条被 head 引用。输家的条目变成无人引用的孤儿，不污染链。
// 如果按 seq 直接定键，两个写入者会互相覆盖同一个键，而 head 只记得赢家的哈希，
// 加载时就会判定「哈希链断裂」——日志永久不可用。
//
// 并发边界的准确表述：
//   - 日志一旦存在（head 有 etag），跨副本并发写靠 etag CAS 串行化，输家 409 重试。
//   - 日志不存在时 etag 为空，Dapr 的 first-write-wins 没有前置条件可用，
//     所以「建日志」必须先于「写事实」单独完成（Ensure），且 Append 拒绝写入空日志。
//     这不是完美方案：多进程同时首次建同一个 PBC 时，进程内 mutex 只能管住本进程，
//     跨进程仍需由创建入口单写者保证（生产上用 Actor 按 PBC 串行是正解）。
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/zhangkun/i3-composer/internal/dapr"
)

// 事实类型。只增不改，命名用「点分名词」。
const (
	FactPBCCreated       = "pbc.created"
	FactArtifactDrafted  = "artifact.drafted"
	FactArtifactAccepted = "artifact.accepted"
	FactCommitCreated    = "commit.created"
	FactTagCreated       = "tag.created"
	FactBuildSucceeded   = "build.succeeded"
	FactTestRun          = "test.run"
	FactDeploySucceeded  = "deploy.succeeded"
	FactItemCreated      = "item.created"
	FactItemRunStarted   = "item.run.started"
	FactItemStepDone     = "item.step.done"
	FactItemWaiting      = "item.waiting"
	FactItemPublished    = "item.published"
	FactItemBlocked      = "item.blocked"
	FactPublishDecided   = "publish.decided"
	FactAnnotationAdded  = "annotation.added"
	FactRoleSwitch       = "role.switch"

	// v2.3：交付单元、BRD、服务验收与 Agent 生命周期。
	// Agent 的定义与评测走独立的 agent 日志（见 ProjectAgents），
	// 因为它属于「数字员工」而不是某个 PBC。
	FactUnitCreated   = "unit.created"
	FactBRDSaved      = "brd.saved"
	FactSvcVerified   = "svc.verified"
	FactAgentDefined  = "agent.defined"
	FactAgentEvalRun  = "agent.eval.run"
	FactAgentPublish  = "agent.published"
	FactAgentRollout  = "agent.rollout"
	FactAgentRollback = "agent.rollback"
	FactAgentBaseline = "agent.baseline"

	// v2.6：Agent 编排（流程图）、构建、试跑与「沉淀为评测用例」。
	// 编排改了但没构建，digest 就对不上——gate 会红，旧评测也不能给新版本背书。
	FactAgentFlowSaved = "agent.flow.saved"
	FactAgentBuilt     = "agent.built"
	FactAgentTrial     = "agent.trial.run"
	FactAgentCase      = "agent.case.added"
	FactAgentRetired   = "agent.retired"
	FactKBIssue        = "kb.issue.dispatched"

	// v2.5/v2.6：真人验收台。清单、判定、bug 与验收对话全部进账本，
	// 因为「验过了」本身就是一条事实，不能只活在前端内存里。
	FactCheckDefined = "check.defined"
	FactCheckDecided = "check.decided"
	FactDefect       = "defect.created"
	FactVerifyChat   = "verify.chat"

	// v2.6：pubsub 的收件事实。发出去的每条事件都在这边留下一条
	// 「收到了」的记录——收发闭环是可查的，不是靠信任。
	FactEventReceived = "event.received"

	// v2.4：客服工单 PBC 与收费缴费重复，从演示项目里删除。
	FactUnitRemoved = "unit.removed"

	// v2.7：工作空间 → 项目。项目是承接容器（一份 BRD + 多个交付单元），
	// 工作空间是文件夹，只做归集；两者都可以「新建」或「加入已有」。
	FactWorkspaceCreated = "workspace.created"
	FactWorkspaceJoined  = "workspace.joined"
	FactProjectCreated   = "project.created"
	FactProjectJoined    = "project.joined"

	// v3.2：单元只做到「可以交付」。机器跑完五步就停，人打 tag 进池；
	// 上哪个环境、和谁一起上，在项目级「发布编排」里定，单元不再自己发布。
	FactItemVerified = "item.verified"
	// 批次是发布的单位：一个批次锁多个 tag 与 digest，只能整批回滚。
	FactReleaseBatch    = "release.batch.created"
	FactReleaseRollback = "release.batch.rolledback"
)

// Fact 是一条不可变事实。
type Fact struct {
	Seq     int             `json:"seq"`
	At      string          `json:"at"`
	Type    string          `json:"type"`
	Actor   string          `json:"actor"` // 人角色（BA/PA/DA/SA）或 X-* worker
	PBC     string          `json:"pbc"`
	OpID    string          `json:"opId,omitempty"` // activity 幂等键；同一 OpID 只落一条事实
	Payload json.RawMessage `json:"payload"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

// head 是日志游标，靠 etag CAS 保证同一 PBC 只有一个写入者。
type head struct {
	Seq  int    `json:"seq"`
	Hash string `json:"hash"`
}

// Store 是某个 app-id 下的事实日志。
type Store struct {
	c        *dapr.Client
	state    string     // state store 组件名
	prefix   string     // 账本前缀：同一套 Dapr 上跑多套账本时用它隔开
	mu       sync.Mutex // 进程内降噪；跨副本靠 etag CAS
	maxRetry int

	// Notify 在一条事实真正落链后回调，用于把「有变化了」推给 BFF 的 SSE 订阅者。
	// 它只做通知，不携带账本内容；进程重启后自行丢失也无妨——下一次读取仍是全量。
	// 必须在开始服务前一次性赋值；之后只读，故不需要额外同步。
	Notify func(pbc string)
}

// New 造一个账本。prefix 为空时键就是 "log:<pbc>:…"；
// 给了前缀则是 "<prefix>/log:<pbc>:…"——同一套 Dapr 上跑隔离环境时用。
func New(c *dapr.Client, stateStore string, prefix ...string) *Store {
	p := ""
	if len(prefix) > 0 {
		p = prefix[0]
	}
	return &Store{c: c, state: stateStore, prefix: p, maxRetry: 32}
}

// Prefix 是账本前缀（空表示默认账本）。
func (s *Store) Prefix() string { return s.prefix }

func (s *Store) headKey(pbc string) string { return s.prefix + "log:" + pbc + ":head" }

// factKey 用 seq + 哈希共同定位：并发写同一 seq 时互不覆盖。
func (s *Store) factKey(pbc string, seq int, hash string) string {
	return s.prefix + "log:" + pbc + ":" + fmt.Sprintf("%08d", seq) + ":" + shortHash(hash)
}

func shortHash(hash string) string {
	h := hash
	if len(h) > 7 && h[:7] == "sha256:" {
		h = h[7:]
	}
	if len(h) > 16 {
		h = h[:16]
	}
	return h
}

// Ensure 建日志。head 写成 seq=0 的空游标，目的是让之后的 Append 有 etag 可用——
// etag 为空的写入在 first-write-wins 下没有前置条件，并发时会互相覆盖。
// 已存在则原样返回，可重复调用。
func (s *Store) Ensure(ctx context.Context, pbc string) error {
	_, etag, err := s.c.GetState(ctx, s.state, s.headKey(pbc))
	if err != nil {
		return err
	}
	if etag != "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 双检：拿锁期间别人可能已经建好了。
	if _, etag, err = s.c.GetState(ctx, s.state, s.headKey(pbc)); err != nil {
		return err
	}
	if etag != "" {
		return nil
	}
	_, err = s.c.SetState(ctx, s.state, s.headKey(pbc), head{Seq: 0, Hash: ""}, "")
	return err
}

// Append 追加一条事实并返回它。失败自动重试（etag 冲突说明有并发写入者，
// 重新读 head 再算哈希即可，无需上锁）。
//
// opID 非空时做幂等去重：Dapr activity 是「至少一次」语义，同一个 OpID 被重放
// 不会产生第二条事实。去重靠扫日志，调用方通常已经读过日志，成本可忽略。
func (s *Store) Append(ctx context.Context, pbc, typ, actor string, payload any) (Fact, error) {
	return s.AppendOp(ctx, pbc, "", typ, actor, payload)
}

func (s *Store) AppendOp(ctx context.Context, pbc, opID, typ, actor string, payload any) (Fact, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Fact{}, err
	}
	for attempt := 0; attempt < s.maxRetry; attempt++ {
		f, err := s.appendOnce(ctx, pbc, opID, typ, actor, raw)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, errNoLog) {
			// 日志还没建：建一次再重试同一个 seq。建日志本身是幂等的。
			if e := s.Ensure(ctx, pbc); e != nil {
				return Fact{}, e
			}
			continue
		}
		if !errors.Is(err, dapr.ErrConflict) {
			return Fact{}, err
		}
		// 指数退避，避免两个写入者互相踩：5ms → 至多 200ms。
		d := time.Duration(5*(1<<min(attempt, 6))) * time.Millisecond
		if d > 200*time.Millisecond {
			d = 200 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return Fact{}, ctx.Err()
		case <-time.After(d):
		}
	}
	return Fact{}, fmt.Errorf("append 放弃：%d 次 etag 冲突，写入并发过高", s.maxRetry)
}

// errNoLog 表示日志尚未建立（head 不存在）。此时 etag 为空，
// first-write-wins 无法串行化并发写，所以不能直接写。
var errNoLog = errors.New("store: 日志未建立")

func (s *Store) appendOnce(ctx context.Context, pbc, opID, typ, actor string, payload json.RawMessage) (Fact, error) {
	rawHead, etag, err := s.c.GetState(ctx, s.state, s.headKey(pbc))
	if err != nil {
		return Fact{}, err
	}
	if etag == "" {
		return Fact{}, errNoLog
	}
	var h head
	if len(rawHead) > 0 {
		if err := json.Unmarshal(rawHead, &h); err != nil {
			return Fact{}, fmt.Errorf("head 损坏 %s: %w", pbc, err)
		}
	}

	// 幂等：同一 OpID 已经落过，直接返回那条事实。
	if opID != "" && h.Seq > 0 {
		done, err := s.findOp(ctx, pbc, h, opID)
		if err != nil {
			return Fact{}, err
		}
		if done != nil {
			return *done, nil
		}
	}

	f := Fact{
		Seq:     h.Seq + 1,
		At:      nowRFC3339(),
		Type:    typ,
		Actor:   actor,
		PBC:     pbc,
		OpID:    opID,
		Payload: payload,
		Prev:    h.Hash,
	}
	f.Hash = hashFact(f)

	// 先写事实，再推进 head。崩在中间只会留下一条无人引用的孤儿事实；
	// 并发输家写的事实落在另一个哈希键上，同样不会被 head 引用。
	if _, err := s.c.SetState(ctx, s.state, s.factKey(pbc, f.Seq, f.Hash), f, ""); err != nil {
		return Fact{}, err
	}
	if _, err := s.c.SetState(ctx, s.state, s.headKey(pbc),
		head{Seq: f.Seq, Hash: f.Hash}, etag); err != nil {
		return Fact{}, err
	}
	// 只在 head 推进成功后通知：没落链的事实不算「有变化」。
	if s.Notify != nil {
		s.Notify(pbc)
	}
	return f, nil
}

// findOp 在链上找一条带该 OpID 的事实。只看最近 64 条即可：
// 一个 workflow 实例的步骤是连着的，重放也不会跑到很远的地方。
func (s *Store) findOp(ctx context.Context, pbc string, h head, opID string) (*Fact, error) {
	facts, err := s.loadChain(ctx, pbc, h, 64)
	if err != nil {
		return nil, err
	}
	for i := range facts {
		if facts[i].OpID == opID {
			return &facts[i], nil
		}
	}
	return nil, nil
}

// Load 读出某个 PBC 的全部事实，按 seq 升序。遇哈希链断裂立即报错——
// 宁可服务不可用，也不能把被篡改的日志当成真的。
func (s *Store) Load(ctx context.Context, pbc string) ([]Fact, error) {
	rawHead, _, err := s.c.GetState(ctx, s.state, s.headKey(pbc))
	if err != nil {
		return nil, err
	}
	if len(rawHead) == 0 {
		return nil, nil
	}
	var h head
	if err := json.Unmarshal(rawHead, &h); err != nil {
		return nil, fmt.Errorf("head 损坏 %s: %w", pbc, err)
	}
	return s.loadChain(ctx, pbc, h, 0)
}

// loadChain 从 head 往回走：每条事实的 Prev 就是前一条的哈希，所以逆向天然可走，
// 正向不可走（不知道下一条的哈希）。倒序读完再反转。
func (s *Store) loadChain(ctx context.Context, pbc string, h head, limit int) ([]Fact, error) {
	if h.Seq == 0 {
		return nil, nil
	}
	seq, hash := h.Seq, h.Hash
	back := make([]Fact, 0, 32)
	for seq >= 1 {
		raw, _, err := s.c.GetState(ctx, s.state, s.factKey(pbc, seq, hash))
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("日志断档：%s seq=%d 缺失", pbc, seq)
		}
		var f Fact
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, err
		}
		if f.Seq != seq || f.Hash != hash {
			return nil, fmt.Errorf("事实定位不符：%s seq=%d 读到 seq=%d", pbc, seq, f.Seq)
		}
		if got := hashFact(f); got != f.Hash {
			return nil, fmt.Errorf("事实被篡改：%s seq=%d hash 不符", pbc, seq)
		}
		back = append(back, f)
		if limit > 0 && len(back) >= limit {
			return reverse(back), nil
		}
		hash, seq = f.Prev, seq-1
	}
	if hash != "" {
		return nil, fmt.Errorf("哈希链根不合法：%s 最早一条的 prev=%q，应为空", pbc, hash)
	}
	return reverse(back), nil
}

func reverse(fs []Fact) []Fact {
	out := make([]Fact, len(fs))
	for i, f := range fs {
		out[len(fs)-1-i] = f
	}
	return out
}

// HasOp 判定一组已加载的事实里是否已有该 OpID。
func HasOp(facts []Fact, opID string) bool {
	if opID == "" {
		return false
	}
	for i := range facts {
		if facts[i].OpID == opID {
			return true
		}
	}
	return false
}

// hashFact 计算事实哈希。字段顺序固定、payload 先做规范化（key 排序），
// 否则同一份内容在不同进程上会算出不同哈希。
func hashFact(f Fact) string {
	canon := canonicalJSON(f.Payload)
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s|%s",
		f.Prev, f.Seq, f.At, f.Type, f.Actor, f.PBC, f.OpID, canon)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON 把 JSON 对象按键排序后重新序列化，保证哈希稳定。
func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return marshalCanonical(v)
}

func marshalCanonical(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			kb, _ := json.Marshal(k)
			b = append(b, kb...)
			b = append(b, ':')
			b = append(b, marshalCanonical(t[k])...)
		}
		return string(append(b, '}'))
	case []any:
		b := []byte{'['}
		for i, x := range t {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, marshalCanonical(x)...)
		}
		return string(append(b, ']'))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// nowRFC3339 单独抽出来，方便测试注入。业务判断不使用它。
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// String 便于日志输出。
func (f Fact) String() string {
	return f.PBC + "#" + strconv.Itoa(f.Seq) + " " + f.Type
}
