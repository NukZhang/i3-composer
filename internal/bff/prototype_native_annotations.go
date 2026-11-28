package bff

import (
	"bytes"
	_ "embed"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

//go:embed prototype_native_annotations.js
var nativeAnnotationBridge string

var nativeAnnotationScript = []byte("<script data-i3-native-annotations>" + nativeAnnotationBridge + "</script>")

func nativeHTMLWithAnnotations(source []byte) []byte {
	offset := nativeAnnotationScriptOffset(source)
	// 交互页添加平台脚本，原件的字节与摘要、只读快照仍从原制品读取。
	result := make([]byte, 0, len(source)+len(nativeAnnotationScript))
	result = append(result, source[:offset]...)
	result = append(result, nativeAnnotationScript...)
	return append(result, source[offset:]...)
}

func nativeAnnotationScriptOffset(source []byte) int {
	bom := []byte{0xef, 0xbb, 0xbf}
	fallback := 0
	if bytes.HasPrefix(source, bom) {
		fallback = len(bom)
	}
	z := html.NewTokenizer(bytes.NewReader(source[fallback:]))
	offset := fallback
	for {
		kind := z.Next()
		offset += len(z.Raw())
		switch kind {
		case html.DoctypeToken:
			// 缺少显式 head 时也保留 DOCTYPE，避免预览进入怪异模式。
			fallback = offset
		case html.StartTagToken:
			switch z.Token().DataAtom {
			case atom.Head:
				return offset
			case atom.Html:
				continue
			}
			return fallback
		case html.ErrorToken, html.EndTagToken, html.SelfClosingTagToken:
			return fallback
		}
	}
}
