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
	Status  string `json:"status"`        // deployed | activated
}

// ReleaseBatch 是一个发布批次。
type ReleaseBatch struct {
	ID         string        `json:"id"`
	Proj       string        `json:"proj"`
	Env        string        `json:"env"`
	Mode       string        `json:"mode"` // full | gray
	Units      []ReleaseUnit `json:"units"`
	By         string        `json:"by"`
	At         string        `json:"at"`
	Status     string        `json:"status"` // active | rolledback
	RollbackAt string        `json:"rollbackAt,omitempty"`
	RollbackBy string        `json:"rollbackBy,omitempty"`
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
				}
			}
		}
	}
	return v, nil
}
