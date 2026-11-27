package domain

import (
	"regexp"
	"strings"
)

var (
	checkHeader = regexp.MustCompile(`^(?:(?:#{1,6}|[-*+])[ \t]+)?((?:AT|AC)-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*)(?:[ \t：:]+|$)(.*)$`)
	checkParts  = regexp.MustCompile(`(?i)^(.*?)\bGiven\b[ :：]*(.*?)\bWhen\b[ :：]*(.*?)\bThen\b[ :：]*(.*)$`)
)

// ChecksFromAT 读取带编号的 Given/When/Then。任何一条缺项或重复时整份待补，
// 避免把部分清单误当成完整验收；围栏中的示例不参与。
func ChecksFromAT(md string) []Check {
	var checks []Check
	var id string
	var lines []string
	seen := map[string]bool{}
	finish := func() bool {
		if id == "" {
			return true
		}
		parts := checkParts.FindStringSubmatch(strings.Join(lines, " "))
		if parts == nil || seen[id] {
			return false
		}
		for i := 1; i < len(parts); i++ {
			parts[i] = strings.Trim(parts[i], " \t，,；;。")
		}
		if parts[2] == "" || parts[3] == "" || parts[4] == "" {
			return false
		}
		name := parts[1]
		if name == "" {
			name = id
		}
		checks = append(checks, Check{ID: id, N: name, G: parts[2], W: parts[3], T: parts[4], ST: CheckTodo})
		seen[id] = true
		id, lines = "", nil
		return true
	}
	fence := ""
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			if !finish() {
				return nil
			}
			fence = line[:3]
			continue
		}
		if header := checkHeader.FindStringSubmatch(line); header != nil {
			if !finish() {
				return nil
			}
			id = header[1]
			lines = append(lines, header[2])
		} else if id != "" {
			if strings.HasPrefix(line, "#") || (line == "" && checkParts.MatchString(strings.Join(lines, " "))) {
				if !finish() {
					return nil
				}
			} else if line != "" {
				lines = append(lines, strings.TrimLeft(line, "-+* \t"))
			}
		}
	}
	if !finish() {
		return nil
	}
	return checks
}
