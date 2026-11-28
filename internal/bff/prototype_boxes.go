package bff

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/zhangkun/i3-composer/internal/store"
)

var prototypeScrollAnchor = regexp.MustCompile(`^[0-9]{1,6}(/[0-9]{1,6})*$`)

func setPrototypeBox(dst *store.Anno, geometry store.Anno) error {
	right, bottom := geometry.X+geometry.W, geometry.Y+geometry.H
	// 用整数端点落链，确保 JSON 重编码后仍能精确回放框的位置和大小。
	const precision = 1_000_000
	const edgeTolerance = 1e-9
	if geometry.X < 0 || geometry.Y < 0 || geometry.W <= 0 || geometry.H <= 0 ||
		right > 1+edgeTolerance || bottom > 1+edgeTolerance {
		return fmt.Errorf("%w：批注框需要落在预览区域内", errBadRequest)
	}
	pixel := func(v float64) int { return int(math.Round(v * precision)) }
	left, top := pixel(geometry.X), pixel(geometry.Y)
	width, height := pixel(right)-left, pixel(bottom)-top
	if width <= 0 || height <= 0 {
		return fmt.Errorf("%w：批注框的宽度和高度必须大于 0", errBadRequest)
	}
	dst.X, dst.Y = float64(left)/precision, float64(top)/precision
	dst.W, dst.H = float64(width)/precision, float64(height)/precision
	return nil
}

func decodePrototypeBoxScope(raw json.RawMessage) (*store.AnnoPreview, error) {
	var scope struct{ Route, Anchor *string }
	err := json.Unmarshal(raw, &scope)
	if err != nil || scope.Route == nil || scope.Anchor == nil {
		return nil, fmt.Errorf("%w：批注框需要完整的交互页定位", errBadRequest)
	}
	route, anchor := *scope.Route, *scope.Anchor
	if len(route) > 1024 || (route != "" && !strings.HasPrefix(route, "#")) ||
		strings.IndexFunc(route, unicode.IsControl) >= 0 || strings.Contains(route, "?") ||
		len(anchor) > 256 || (anchor != "" && !prototypeScrollAnchor.MatchString(anchor)) {
		return nil, fmt.Errorf("%w：交互页位置格式无效", errBadRequest)
	}
	return &store.AnnoPreview{Route: route, Anchor: anchor}, nil
}
