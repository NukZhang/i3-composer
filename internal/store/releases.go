package store

import (
	"encoding/json"
	"fmt"
)

// 发布编排是项目级的动作（v3.2）：单元只做到「可以交付」并打 tag 进池，
// 上哪个环境、和谁一起上，由批次决定。
//
// 批次是发布的单位，不是单元：
//   - 一个批次锁住若干单元的 tag 与 digest，生成后不可改；
//   - 回滚只能整批回——只回一半会留下说不清的组合；
//   - 没打 tag 的单元进不来，也不会被「顺手带上」。

// ReleaseUnit 是批次里锁定的一个交付物。
type ReleaseUnit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	AgentID string `json:"agentId,omitempty"`
	Tag     string `json:"tag"`
	Commit  string `json:"commit"`
	Digest  string `json:"digest"`
	Binary  bool   `json:"binary"`
	URL     string `json:"url,omitempty"` // Agent 产物没有进程，就是空
	Status  string `json:"status"`        // deployed | activated | rolledback
}

// ReleaseBatch 是一个发布批次。
type ReleaseBatch struct {
	ID     string        `json:"id"`
	Proj   string        `json:"proj"`
	Env    string        `json:"env"`
	Mode   string        `json:"mode"` // full | gray
	Units  []ReleaseUnit `json:"units"`
	By     string        `json:"by"`
	At     string        `json:"at"`
	Status string        `json:"status"` // active | rolledback | failed
	// Reason 只在 failed 时有：哪一步没成。失败批次也留在账上——
	// 以前它连记录都没有，账本于是留下「success、无批次引用、指向死进程」的部署事实（#41）。
	Reason     string `json:"reason,omitempty"`
	RollbackAt string `json:"rollbackAt,omitempty"`
	RollbackBy string `json:"rollbackBy,omitempty"`
}

// ReleasesView 是 releases 日志的投影。批次新的在前。
type ReleasesView struct {
	Seq     int            `json:"seq"`
	Batches []ReleaseBatch `json:"batches"`
}

// ProjectReleases 重放 releases 日志。纯函数：不读时钟、不访问网络。
func ProjectReleases(facts []Fact) (*ReleasesView, error) {
	v := &ReleasesView{Seq: len(facts), Batches: []ReleaseBatch{}}
	for _, f := range facts {
		switch f.Type {
		case FactReleaseBatch:
			var b ReleaseBatch
			if err := json.Unmarshal(f.Payload, &b); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			if b.ID == "" || len(b.Units) == 0 {
				continue
			}
			if b.Status == "" {
				b.Status = "active"
			}
			v.Batches = append([]ReleaseBatch{b}, v.Batches...)
		case FactReleaseProgress:
			// 只记「这一批已部署到第几个单元」，不改锁定清单：tag / digest 是发布时定的。
			// 必须带 unit：以前按「第一个还没有 URL 的单元」填，Agent 单元没有 URL、
			// 也不会收到 progress（它不部署），于是 PBC 的地址被填到了排在它前面的 Agent 名下——
			// 单元账本写着「上线了」，批次里那个 PBC 的 url 却是空，回滚时按 url 判空就跳过了它，
			// 进程留在机器上（#41 自测遇过）。progress 自己说清是哪个单元。
			var p struct {
				ID   string `json:"id"`
				Unit string `json:"unit"`
				URL  string `json:"url"`
				At   string `json:"at"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			for i := range v.Batches {
				if v.Batches[i].ID != p.ID {
					continue
				}
				for j := range v.Batches[i].Units {
					u := &v.Batches[i].Units[j]
					// 新事实带 unit，按名字认；老事实（没有 unit）按「第一个还没有 URL 的」认，
					// 已有记录的重放结果不变。
					if p.Unit != "" && u.ID != p.Unit {
						continue
					}
					if p.Unit == "" && u.URL != "" {
						continue
					}
					if p.URL != "" {
						u.URL = p.URL
					}
					u.Status = "deployed"
					break
				}
			}
		case FactReleaseFailed:
			var p struct {
				ID     string `json:"id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			for i := range v.Batches {
				if v.Batches[i].ID == p.ID {
					v.Batches[i].Status = "failed"
					v.Batches[i].Reason = p.Reason
				}
			}
		case FactReleaseRollback:
			var p struct {
				ID string `json:"id"`
				By string `json:"by"`
				At string `json:"at"`
			}
			if err := json.Unmarshal(f.Payload, &p); err != nil {
				return nil, fmt.Errorf("重放 %s 失败：%w", f, err)
			}
			for i := range v.Batches {
				if v.Batches[i].ID == p.ID {
					v.Batches[i].Status = "rolledback"
					v.Batches[i].RollbackAt = p.At
					v.Batches[i].RollbackBy = p.By
					// 回滚是整批的：批次里每个真正上线的单元都随之撤销。
					// 以前只改批次状态，单元行还写着 deployed、URL 还在，编排页读起来像「还在线上」（#29）。
					for j := range v.Batches[i].Units {
						if v.Batches[i].Units[j].Status == "deployed" {
							v.Batches[i].Units[j].Status = "rolledback"
						}
					}
				}
			}
		}
	}
	return v, nil
}
