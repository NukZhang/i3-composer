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
	// Unknown 表示这条闸「判不了」，而不是「没通过」。
	//
	// 差别很重要：视觉比对还没接时，那句「无未处理差异」是猜的，
	// 不是算出来的。旧实现直接按 unhandled==0 判绿，页面就把
	// 「没比对过」显示成「比过了、没差异」。判不了就说判不了。
	Unknown bool `json:"unknown,omitempty"`
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
//
// 传 nil 表示「还没比对过」，与「比对过、0 处差异」是两件事：
// 前者这条闸判不了（Unknown），后者才算真的过了。
func CheckCommit(c Commit, versions map[Artifact][]Version, unhandled *int) []Gate {
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

	// 闸四：原型还原。没有比对结果就不判——判不了就如实说判不了，
	// 不能拿「未处理差异数默认为 0」假装比过了。
	visual := Gate{Key: "visual", Label: "原型还原", OK: true, Unknown: true,
		Detail: "视觉比对未接入：这条判不了，别当成证据"}
	if unhandled != nil {
		visual.OK = *unhandled == 0
		visual.Unknown = false
		visual.Detail = detail(visual.OK, "无未处理差异",
			fmt.Sprintf("还有 %d 处差异未处理", *unhandled))
	}

	return []Gate{
		{Key: "closure", Label: "引用闭合", OK: closed,
			Detail: detail(closed, "无悬空引用", firstMiss)},
		{Key: "accepted", Label: "制品全部 accepted", OK: allAccepted,
			Detail: detail(allAccepted, "无草稿", "存在未接受版本")},
		{Key: "digest", Label: "镜像与树一致", OK: c.Digest != "",
			Detail: detail(c.Digest != "", c.Digest, "未构建，无 digest")},
		visual,
	}
}

// GatesPass 判定四闸是否全过。
//
// Unknown 的闸不挡发布：它表示这条「判不了」，不是「没过」。
// 但要让人知道哪几条判不了——Gate.Unknown 就是给这个用的。
func GatesPass(gs []Gate) bool {
	for _, g := range gs {
		if !g.OK {
			return false
		}
	}
	return true
}

// NextTag 基于已有 tag 列表算出下一个 tag：v1.0.0 → v1.0.1。
//
// 按 (major, minor, patch) 整个元组取最大，再把 patch 进一位。只看 patch 的话，
// 一条手工打的 v2.0.0 会被算成 v1.0.1——比现有 tag 还小，撞上旧号就是「回滚指向不明」。
// tag 是交付凭据，这里只能往前走：结果永远大于列表里的每一个合法 tag。
// 解析不了的 tag 跳过；一个都解析不出来时从 v1.0.0 起。
func NextTag(tags []string) string {
	var best [3]int
	found := false
	for _, t := range tags {
		var major, minor, patch int
		// "v1.2" 这种缺一段的写法 n=2 且 err != nil，正好一并跳过。
		if n, err := fmt.Sscanf(t, "v%d.%d.%d", &major, &minor, &patch); n != 3 || err != nil {
			continue
		}
		if major < 0 || minor < 0 || patch < 0 {
			continue
		}
		v := [3]int{major, minor, patch}
		if !found || lessTuple(best, v) {
			best, found = v, true
		}
	}
	if !found {
		return "v1.0.0"
	}
	return fmt.Sprintf("v%d.%d.%d", best[0], best[1], best[2]+1)
}

// lessTuple 是三元组的字典序比较，避开每个版本号都要 import slices 的麻烦。
func lessTuple(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
