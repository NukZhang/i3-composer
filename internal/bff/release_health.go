package bff

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/zhangkun/i3-composer/internal/deployer"
	"github.com/zhangkun/i3-composer/internal/store"
)

type releaseUnitView struct {
	store.ReleaseUnit
	HealthURL string `json:"healthUrl,omitempty"`
}

type releaseBatchView struct {
	store.ReleaseBatch
	Units []releaseUnitView `json:"units"`
}

func (s *Server) releaseBatchView(batch store.ReleaseBatch) releaseBatchView {
	view := releaseBatchView{ReleaseBatch: batch, Units: make([]releaseUnitView, 0, len(batch.Units))}
	for _, unit := range batch.Units {
		entry := releaseUnitView{ReleaseUnit: unit}
		if _, _, ok := s.releaseUnitRuntime(batch, unit); ok {
			entry.HealthURL = "/api/projects/" + url.PathEscape(batch.Proj) + "/releases/" +
				url.PathEscape(batch.ID) + "/units/" + url.PathEscape(unit.ID) + "/health"
		}
		view.Units = append(view.Units, entry)
	}
	return view
}

func (s *Server) releaseUnitInstance(batch store.ReleaseBatch, unit store.ReleaseUnit) (deployer.Instance, bool) {
	if batch.Status != "active" || !unit.Binary || unit.URL == "" || unit.RuntimeID == "" || s.Pipe == nil || s.Pipe.Dep == nil {
		return deployer.Instance{}, false
	}
	instance, ok := s.Pipe.Dep.Instance(unit.ID, batch.Env)
	// 实例启动与批次落链可能交错，因此以冻结的实例标识为准。
	if !ok || instance.Commit != unit.Commit || instance.Digest != unit.Digest || instance.URL != unit.URL ||
		unit.RuntimeID != instance.RuntimeID {
		return deployer.Instance{}, false
	}
	return instance, true
}

func (s *Server) releaseUnitRuntime(batch store.ReleaseBatch, unit store.ReleaseUnit) (deployer.Instance, string, bool) {
	instance, ok := s.releaseUnitInstance(batch, unit)
	if !ok {
		return deployer.Instance{}, "", false
	}
	endpoint := releaseHealthEndpoint(instance.URL)
	return instance, endpoint, endpoint != ""
}

func releaseHealthEndpoint(address string) string {
	target, err := url.Parse(address)
	if err != nil || target.Scheme != "http" || target.User != nil || !net.ParseIP(target.Hostname()).IsLoopback() ||
		target.Port() == "" || (target.Path != "" && target.Path != "/") || target.RawQuery != "" || target.Fragment != "" {
		return ""
	}
	target.Path, target.RawPath = "/healthz", ""
	return target.String()
}

func (s *Server) releaseUnitHealth(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	w.Header().Set("Cache-Control", "no-store")
	if err := Require(c.Role, ActView); err != nil {
		return err
	}
	releases, err := s.loadReleases(r.Context())
	if err != nil {
		return err
	}
	for _, batch := range releases.Batches {
		if batch.Proj != r.PathValue("id") || batch.ID != r.PathValue("batchID") {
			continue
		}
		for _, unit := range batch.Units {
			if unit.ID == r.PathValue("unitID") {
				return s.verifyReleaseUnit(w, r, batch, unit)
			}
		}
		break
	}
	return fmt.Errorf("%w：批次不包含该单元", errNotFound)
}

func (s *Server) verifyReleaseUnit(w http.ResponseWriter, r *http.Request, batch store.ReleaseBatch, unit store.ReleaseUnit) error {
	instance, endpoint, ok := s.releaseUnitRuntime(batch, unit)
	if !ok {
		return fmt.Errorf("%w：该批次的 PBC 进程未运行或已替换", errNotFound)
	}
	probe, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(probe)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "PBC 健康检查无响应"})
		return nil
	}
	defer response.Body.Close()
	var health struct {
		OK     bool   `json:"ok"`
		Unit   string `json:"unit"`
		Commit string `json:"commit"`
	}
	if response.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health) != nil ||
		!health.OK || health.Unit != unit.ID || (health.Commit != "" && health.Commit != unit.Commit) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "PBC 健康检查未通过或返回的单元、版本不匹配"})
		return nil
	}
	if current, _, alive := s.releaseUnitRuntime(batch, unit); !alive || current.RuntimeID != instance.RuntimeID {
		return fmt.Errorf("%w：该批次的 PBC 进程已停止或替换", errNotFound)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "project": batch.Proj, "batch": batch.ID, "environment": batch.Env,
		"unit": unit.ID, "commit": unit.Commit, "tag": unit.Tag, "digest": unit.Digest,
	})
	return nil
}
