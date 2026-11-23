package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Gate 是交付四闸之一。原型 L952-957 的 checkCommit 是它的前端版本，
// 这里是唯一权威版本：前端只做展示，服务端不通过就不许打 tag。
type Gate struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Commit 是一个原子提交：整棵树落一个版本号，全落或全不落。
type Commit struct {
	No      string `json:"no"`
	Tree    Tree   `json:"tree"`
	By      string `json:"by"`
	At      string `json:"at"`
	Msg     string `json:"msg"`
	Type    string `json:"type"`
	Digest  string `json:"digest,omitempty"` // 构建产物 sha256；空表示尚未构建
	TreeSHA string `json:"treeSha"`
	Tag     string `json:"tag,omitempty"`
}

// TreeSHA 计算树摘要。必须是确定性的：key 排序后再哈希，不能依赖 map 迭代序。
func TreeSHA(t Tree) string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, t[Artifact(k)])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:12]
}

// CheckCommit 判定一次提交是否满足交付四闸。
//
// unhandled 是「原型还原未处理差异数」，由 BFF 从视觉比对结果传入——
// 本包不做图像比对，只消费它的结论，保持纯函数。
func CheckCommit(c Commit, versions map[Artifact][]Version, unhandled int) []Gate {
	// 闸一：引用闭合。树里每个版本都得存在，且它的衍生引用与树一致。
	closed := true
	var miss []string
	for _, t := range Order {
		v, exists := c.Tree[t]
		if !exists || v == "" {
			closed = false
			miss = append(miss, fmt.Sprintf("%s 未入树", t))
			continue
		}
		ver, found := Find(versions, t, v)
		if !found {
			closed = false
			miss = append(miss, fmt.Sprintf("%s %s 不存在", t, v))
			continue
		}
		for _, d := range ver.DF {
			if c.Tree[d.T] != d.V {
				closed = false
				miss = append(miss, fmt.Sprintf("%s→%s %s", t, d.T, d.V))
			}
		}
	}

	// 闸二：树里所有版本都必须是 accepted。草稿不许进交付。
	allAccepted := true
	for _, t := range Order {
		ver, found := Find(versions, t, c.Tree[t])
		if !found || ver.St != StAccepted {
			allAccepted = false
			break
		}
	}

	detail := func(ok bool, good, bad string) string {
		if ok {
			return good
		}
		return bad
	}

	var firstMiss string
	if len(miss) > 0 {
		firstMiss = miss[0]
	}

	return []Gate{
		{Key: "closure", Label: "引用闭合", OK: closed,
			Detail: detail(closed, "无悬空引用", firstMiss)},
		{Key: "accepted", Label: "制品全部 accepted", OK: allAccepted,
			Detail: detail(allAccepted, "无草稿", "存在未接受版本")},
		{Key: "digest", Label: "镜像与树一致", OK: c.Digest != "",
			Detail: detail(c.Digest != "", c.Digest, "未构建，无 digest")},
		{Key: "visual", Label: "原型还原", OK: unhandled == 0,
			Detail: detail(unhandled == 0, "无未处理差异",
				fmt.Sprintf("还有 %d 处差异未处理", unhandled))},
	}
}

// GatesPass 判定四闸是否全过。
func GatesPass(gs []Gate) bool {
	for _, g := range gs {
		if !g.OK {
			return false
		}
	}
	return true
}

// NextTag 基于已有 tag 列表算出下一个 tag：v1.0.0 → v1.0.1。
func NextTag(tags []string) string {
	if len(tags) == 0 {
		return "v1.0.0"
	}
	max := -1
	for _, t := range tags {
		var major, minor, patch int
		if _, err := fmt.Sscanf(t, "v%d.%d.%d", &major, &minor, &patch); err == nil && patch > max {
			max = patch
		}
	}
	if max < 0 {
		return "v1.0.0"
	}
	return fmt.Sprintf("v1.0.%d", max+1)
}
