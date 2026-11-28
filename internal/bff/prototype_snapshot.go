package bff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zhangkun/i3-composer/internal/store"
)

var (
	errPrototypeRendererUnavailable = errors.New("动态 HTML 快照服务未就绪，请稍后重新打开")
	errPrototypeSnapshotFailed      = errors.New("无法生成动态 HTML 快照。请检查文件是否包含可离线渲染的页面")
	prototypeDigest                 = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
)

func (s *Server) readPrototypeSnapshot(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	meta, err := s.prototypeImportForRequest(r, c)
	if err != nil {
		return err
	}
	if meta.MediaType != "text/html" {
		return fmt.Errorf("%w：只有 HTML 原型支持生成快照", errBadRequest)
	}
	if s.Pipe == nil || s.Pipe.Store == nil {
		return errors.New("原件制品仓库未配置")
	}
	parts := prototypeDigest.FindStringSubmatch(meta.Digest)
	if parts == nil {
		return errors.New("原型原件摘要无效")
	}
	// 坐标归一化到同一张图。图一旦生成就固定在原件摘要下，刷新或重启仍沿用。
	name := filepath.Join(s.Pipe.Store.Root(), "prototype-snapshots", parts[1]+".png")
	snapshot, err := s.prototypeSnapshot(r.Context(), name, meta)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "prototype.png", time.Time{}, bytes.NewReader(snapshot))
	return nil
}

func (s *Server) prototypeSnapshot(ctx context.Context, name string, meta store.PrototypeImport) ([]byte, error) {
	if cached, err := os.ReadFile(name); err == nil || !errors.Is(err, os.ErrNotExist) {
		return cached, err
	}
	if s.PrototypeRenderSocket == "" || !s.prototypeSnapshotMu.TryLock() {
		return nil, errPrototypeRendererUnavailable
	}
	defer s.prototypeSnapshotMu.Unlock()
	if cached, err := os.ReadFile(name); err == nil || !errors.Is(err, os.ErrNotExist) {
		return cached, err
	}
	original, err := s.prototypeOriginalHTML(meta)
	if err != nil {
		return nil, err
	}
	result, err := renderPrototypeSnapshot(ctx, s.PrototypeRenderSocket, original)
	if err != nil {
		return nil, err
	}
	if err := savePrototypeSnapshot(name, result); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ReadFile(name)
		}
		slog.Warn("保存原型快照失败", "err", err)
		return nil, errors.New("保存原型快照失败")
	}
	return result, nil
}

func (s *Server) prototypeOriginalHTML(meta store.PrototypeImport) ([]byte, error) {
	if s.Pipe == nil || s.Pipe.Store == nil {
		return nil, errors.New("原件制品仓库未配置")
	}
	original, err := s.Pipe.Store.Get(meta.Digest)
	if err != nil || int64(len(original)) != meta.Size || len(original) > prototypeFileLimit {
		return nil, errors.New("读取原型原件失败")
	}
	return original, nil
}

func requestPrototypeRenderer(ctx context.Context, socket, method, path, mediaType string, body []byte) (*http.Response, error) {
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	// 只向固定的本地 socket 发送原件或动作，身份和请求查询参数留在工作台。
	req, err := http.NewRequestWithContext(ctx, method, "http://prototype-renderer"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if mediaType != "" {
		req.Header.Set("Content-Type", mediaType)
	}
	return client.Do(req)
}

func readPrototypeRendererPNG(res *http.Response) ([]byte, error) {
	if res.Header.Get("Content-Type") != "image/png" {
		return nil, errPrototypeSnapshotFailed
	}
	result, err := io.ReadAll(io.LimitReader(res.Body, prototypeFileLimit+1))
	if err != nil || len(result) > prototypeFileLimit {
		return nil, errPrototypeSnapshotFailed
	}
	return result, nil
}

func renderPrototypeSnapshot(ctx context.Context, socket string, original []byte) ([]byte, error) {
	res, err := requestPrototypeRenderer(ctx, socket, http.MethodPost, "/render", "text/html; charset=utf-8", original)
	if err != nil {
		return nil, errPrototypeRendererUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		return nil, errPrototypeRendererUnavailable
	}
	if res.StatusCode != http.StatusOK {
		return nil, errPrototypeSnapshotFailed
	}
	result, err := readPrototypeRendererPNG(res)
	if err != nil {
		return nil, err
	}
	if _, err := validatePrototypeImage(result, "image/png", png.DecodeConfig, png.Decode); err != nil {
		return nil, errPrototypeSnapshotFailed
	}
	return result, nil
}

func savePrototypeSnapshot(name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".render-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// 其他实例已生成时保留第一张，避免旧线条落点改变。
	return os.Link(f.Name(), name)
}
