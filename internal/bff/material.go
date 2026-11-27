package bff

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/zhangkun/i3-composer/internal/domain"
	"github.com/zhangkun/i3-composer/internal/store"
)

func (s *Server) materialUnit(ctx context.Context, id string) error {
	units, err := s.loadUnits(ctx)
	if err != nil {
		return err
	}
	for _, u := range units.Units {
		if u.ID == id && u.Profile == domain.ProfileMaterialInbound {
			return nil
		}
	}
	return fmt.Errorf("%w：材料入库单元 %s", errNotFound, id)
}

func (s *Server) startMaterialPreview(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	if err := Require(c.Role, ActAcceptAT); err != nil {
		return err
	}
	id := r.PathValue("id")
	if err := s.materialUnit(r.Context(), id); err != nil {
		return err
	}
	if s.Pipe == nil {
		return fmt.Errorf("执行侧未接入，无法启动测试实例")
	}
	s.materialPreviewMu.Lock()
	defer s.materialPreviewMu.Unlock()

	v, err := s.load(r, id)
	if err != nil {
		return err
	}
	if len(v.Commits) == 0 {
		return fmt.Errorf("%w：还没有提交", errBadRequest)
	}
	head := v.Commits[0]
	if head.Digest == "" || !v.Binaries[head.No] || v.Test == nil || v.Test.Commit != head.No ||
		v.Test.Total == 0 || v.Test.Pass != v.Test.Total {
		return fmt.Errorf("%w：当前提交须完成机器测试和业务构建", errBadRequest)
	}
	const env = "test"
	result := map[string]string{"url": materialURL(id, env), "commit": head.No, "digest": head.Digest}
	if run, ok := s.Pipe.Dep.Instance(id, env); ok && run.Commit == head.No && run.Digest == head.Digest {
		writeJSON(w, http.StatusOK, result)
		return nil
	}
	if _, err := s.Pipe.Deploy(r.Context(), id, env, head.No, head.Digest, true); err != nil {
		return err
	}
	if _, err := s.Log.Append(r.Context(), id, store.FactMaterialPreview, string(c.Role), result); err != nil {
		s.Pipe.Stop(id, env)
		return err
	}
	writeJSON(w, http.StatusCreated, result)
	return nil
}

func materialURL(id, env string) string { return "/pbc/" + id + "/" + strings.ToLower(env) + "/" }

func (s *Server) materialRuntime(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	id, env, rest := r.PathValue("id"), r.PathValue("env"), r.PathValue("rest")
	switch env {
	case "test":
	case "sit":
		env = "SIT"
	default:
		return fmt.Errorf("%w：业务运行环境", errNotFound)
	}
	if r.Method == http.MethodPost {
		if err := Require(c.Role, ActAcceptAT); err != nil {
			return err
		}
		typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if typ != "application/json" {
			return fmt.Errorf("%w：入库请求须使用 JSON", errBadRequest)
		}
		rest = "api/inbounds"
	} else if !materialReadPath(rest) {
		return fmt.Errorf("%w：业务运行路径", errNotFound)
	}
	if err := s.materialUnit(r.Context(), id); err != nil {
		return err
	}
	if s.Pipe == nil {
		return fmt.Errorf("%w：业务进程未启动", errNotFound)
	}
	run, ok := s.Pipe.Dep.Instance(id, env)
	if !ok {
		return fmt.Errorf("%w：该环境的业务进程未启动", errNotFound)
	}
	if requested := r.URL.Query().Get("commit"); requested != "" && requested != run.Commit {
		return fmt.Errorf("%w：该批次的版本未在当前环境运行", errNotFound)
	}
	if env == "test" {
		v, err := s.load(r, id)
		if err != nil {
			return err
		}
		if len(v.Commits) == 0 || run.Commit != v.Commits[0].No || run.Digest != v.Commits[0].Digest {
			return fmt.Errorf("%w：请启动当前提交的测试实例", errBadRequest)
		}
	}
	target, err := url.Parse(run.URL)
	if err != nil || target.Scheme != "http" || !net.ParseIP(target.Hostname()).IsLoopback() {
		return fmt.Errorf("业务进程地址无效")
	}
	// 目标由已运行的制品进程决定。只转发业务白名单，工作台会话留在 BFF。
	proxy := httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.URL.Path, req.Out.URL.RawPath = "/"+rest, ""
			req.Out.Header.Del("Cookie")
			req.Out.Header.Del("Authorization")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			resp.Header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "业务进程未响应"})
		},
	}
	proxy.ServeHTTP(w, r)
	return nil
}

func materialReadPath(rest string) bool {
	switch rest {
	case "", "app.js", "style.css", "healthz", "api/options", "api/inbounds":
		return true
	}
	id, ok := strings.CutPrefix(rest, "api/inbounds/")
	return ok && id != "" && !strings.Contains(id, "/")
}
