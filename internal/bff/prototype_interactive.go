package bff

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zhangkun/i3-composer/internal/store"
)

const prototypeInteractiveHeader = "X-Prototype-Session"

var (
	prototypeRendererSession           = regexp.MustCompile(`^[0-9a-f]{32}$`)
	errPrototypeInteractiveExpired     = fmt.Errorf("%w：交互预览已结束，请重新打开", errNotFound)
	errPrototypeInteractiveUnavailable = errors.New("交互预览服务暂不可用，请稍后重试")
	errPrototypeInteractiveFailed      = errors.New("交互预览未能返回画面，请重新打开")
)

type prototypeInteractiveInput struct {
	Type   string   `json:"type"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	DeltaX *float64 `json:"deltaX,omitempty"`
	DeltaY *float64 `json:"deltaY,omitempty"`
	Key    string   `json:"key,omitempty"`
	Text   string   `json:"text,omitempty"`
}

func (s *Server) prototypeInteractive(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return ErrForbidden
	}
	meta, err := s.prototypeImportForRequest(r, c)
	if err != nil {
		return err
	}
	if meta.MediaType != "text/html" {
		return fmt.Errorf("%w：只有 HTML 原型支持交互预览", errBadRequest)
	}
	if s.PrototypeRenderSocket == "" {
		return errPrototypeInteractiveUnavailable
	}

	token := r.PathValue("prototypeSession")
	start := token == ""
	path := "/interactive"
	contentType := ""
	var body []byte
	expected := http.StatusOK
	switch {
	case start:
		if err := decodePrototypeInteractiveJSON(w, r, &struct{}{}); err != nil {
			return err
		}
		body, err = s.prototypeOriginalHTML(meta)
		if err != nil {
			return err
		}
		expected = http.StatusCreated
		contentType = "text/html; charset=utf-8"
	default:
		id, signature, _ := strings.Cut(token, ".")
		if !prototypeRendererSession.MatchString(id) || !hmac.Equal([]byte(signature), []byte(s.prototypeInteractiveSignature(c.PBC, meta, id))) {
			return errPrototypeInteractiveExpired
		}
		path += "/" + id
		if r.Method == http.MethodDelete {
			expected = http.StatusNoContent
		} else {
			body, err = readPrototypeInteractiveInput(w, r)
			if err != nil {
				return err
			}
			contentType = "application/json"
		}
	}

	res, err := requestPrototypeRenderer(r.Context(), s.PrototypeRenderSocket, r.Method, path, contentType, body)
	if err != nil {
		return errPrototypeInteractiveUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != expected {
		switch res.StatusCode {
		case http.StatusNotFound:
			if start {
				return errPrototypeInteractiveUnavailable
			}
			return errPrototypeInteractiveExpired
		case http.StatusServiceUnavailable:
			return errPrototypeInteractiveUnavailable
		default:
			return errPrototypeInteractiveFailed
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if expected == http.StatusNoContent {
		w.WriteHeader(expected)
		return nil
	}

	image, err := readPrototypeRendererPNG(res)
	if err != nil {
		return errPrototypeInteractiveFailed
	}
	dims, err := validatePrototypeImage(image, "image/png", png.DecodeConfig, png.Decode)
	if err != nil || dims.Width != 1200 || dims.Height != 800 {
		return errPrototypeInteractiveFailed
	}
	if start {
		id := res.Header.Get(prototypeInteractiveHeader)
		if !prototypeRendererSession.MatchString(id) {
			return errPrototypeInteractiveFailed
		}
		w.Header().Set(prototypeInteractiveHeader, id+"."+s.prototypeInteractiveSignature(c.PBC, meta, id))
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.WriteHeader(expected)
	_, _ = w.Write(image)
	return nil
}

func (s *Server) prototypeInteractiveSignature(unit string, meta store.PrototypeImport, id string) string {
	// 会话是只在当前页面持有的短期能力凭证，绑定单元及原件，不能借用到其他导入记录。
	return s.Sess.sign(fmt.Sprintf("prototype-interactive:%s:%d:%s:%s", unit, meta.Seq, meta.Digest, id))
}

func decodePrototypeInteractiveJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		return fmt.Errorf("%w：交互预览需要 JSON 请求", errBadRequest)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(target) != nil || !errors.Is(dec.Decode(new(any)), io.EOF) {
		return fmt.Errorf("%w：交互请求格式有误", errBadRequest)
	}
	return nil
}

func readPrototypeInteractiveInput(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var in prototypeInteractiveInput
	if err := decodePrototypeInteractiveJSON(w, r, &in); err != nil {
		return nil, err
	}
	out := prototypeInteractiveInput{Type: in.Type}
	inside := func(n *float64) bool { return n != nil && *n >= 0 && *n < 1 }
	wheel := func(n *float64) bool { return n != nil && *n >= -1600 && *n <= 1600 }
	invalid := fmt.Errorf("%w：不支持的交互动作或参数", errBadRequest)
	switch in.Type {
	case "click", "wheel":
		if !inside(in.X) || !inside(in.Y) {
			return nil, invalid
		}
		out.X, out.Y = in.X, in.Y
		if in.Type == "wheel" {
			if !wheel(in.DeltaX) || !wheel(in.DeltaY) {
				return nil, invalid
			}
			out.DeltaX, out.DeltaY = in.DeltaX, in.DeltaY
		}
	case "key":
		switch in.Key {
		case "Tab", "Shift+Tab", "Enter", "Escape", "Space", "Backspace", "Delete", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown", "Control+a":
			out.Key = in.Key
		default:
			return nil, invalid
		}
	case "text":
		if in.Text == "" || utf8.RuneCountInString(in.Text) > 512 || len(in.Text) > 2048 {
			return nil, invalid
		}
		out.Text = in.Text
	case "frame":
	default:
		return nil, invalid
	}
	return json.Marshal(out)
}
