// Package dapr 是 BFF 与 sidecar 之间唯一的一层：只做协议翻译，不放业务判断。
//
// 为什么不用 go-sdk：这里只需要 5 个 HTTP 端点（state CAS / publish /
// workflow start / raiseEvent / get），标准库足够，少一层版本耦合。
package dapr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrConflict 表示状态写入的 etag 不匹配（并发写输家）。
var ErrConflict = errors.New("dapr: state etag conflict")

// Unreachable 判断一次状态读写是不是「连不上 sidecar」。
// 连接被拒、sidecar 在重启、3xx/5xx 都属于可重试；业务语义错误（409 等）不算。
// 调用方据此决定等一下再试，而不是当成账本坏了。
func Unreachable(err error) bool {
	if err == nil {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, ": 500 ") ||
		strings.Contains(msg, ": 503 ")
}

// Client 是 Dapr sidecar 的 HTTP 客户端。
type Client struct {
	base string
	http *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		base: baseURL,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetState 读一个键，返回原始 JSON 与 etag。键不存在时 etag 为空、raw 为 nil。
func (c *Client) GetState(ctx context.Context, store, key string) (raw []byte, etag string, err error) {
	u := fmt.Sprintf("%s/v1.0/state/%s/%s", c.base, url.PathEscape(store), url.PathEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNoContent || len(body) == 0 || string(body) == "null" {
		return nil, resp.Header.Get("ETag"), nil
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("dapr get state %s/%s: %d %s", store, key, resp.StatusCode, body)
	}
	return body, resp.Header.Get("ETag"), nil
}

// ConcurrencyFirstWrite 是 Dapr 状态写入的并发模型名。
// 取值必须是 Dapr 认的那三个（Dapr 源码 pkg/api/grpc/util.go）：
//   - "first-write" ：带 etag 时 etag 不匹配则 409（我们要的就是这个）
//   - "last-write"  ：无条件覆盖
//
// 注意不能写成 "first-write-wins"——那是概念名，不是协议值，Dapr 会直接 500。
const ConcurrencyFirstWrite = "first-write"

// SetState 写一个键。etag 非空时启用 first-write：不匹配返回 ErrConflict。
// 返回写入后的新 etag（供调用方继续 CAS）。
func (c *Client) SetState(ctx context.Context, store, key string, value any, etag string) (string, error) {
	item := map[string]any{
		"key":   key,
		"value": value,
		"options": map[string]any{
			"concurrency": ConcurrencyFirstWrite,
			"consistency": "strong",
		},
	}
	if etag != "" {
		item["etag"] = etag
	}
	body, err := json.Marshal([]any{item})
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/v1.0/state/%s", c.base, url.PathEscape(store))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusConflict {
		return "", ErrConflict
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("dapr set state %s/%s: %d %s", store, key, resp.StatusCode, respBody)
	}
	// Dapr 在写入响应里不回 etag，读回一次拿到新 etag。
	_, newETag, err := c.GetState(ctx, store, key)
	if err != nil {
		return "", err
	}
	return newETag, nil
}

// Publish 发一条事件。失败不阻断主流程，由调用方决定是否忽略。
func (c *Client) Publish(ctx context.Context, pubsub, topic string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/v1.0/publish/%s/%s", c.base, url.PathEscape(pubsub), url.PathEscape(topic))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("dapr publish %s/%s: %d %s", pubsub, topic, resp.StatusCode, b)
	}
	return nil
}

// AppConnection 是 sidecar 认为「应用在哪」的那一小段元数据。
//
// pubsub 事件、actor 回调都往这里投。它和本进程实际监听的端口是两件事：
// sidecar 由 docker-compose 固定成 8080，业务进程若监听别的端口，
// 事件就会投到别处（验收里表现为「一条都没收到」，很难看出原因）。
type AppConnection struct {
	Port    int    `json:"port"`
	Address string `json:"channelAddress"`
}

// AppConnection 读 sidecar 的 app 连接配置。读不到（sidecar 没起、版本不认）时返回错误，
// 调用方据此区分「没配」与「配错了」。
func (c *Client) AppConnection(ctx context.Context) (AppConnection, error) {
	var zero AppConnection
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1.0/metadata", nil)
	if err != nil {
		return zero, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return zero, fmt.Errorf("dapr metadata: %d %s", resp.StatusCode, body)
	}
	var m struct {
		AppConnectionProperties AppConnection `json:"appConnectionProperties"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return zero, fmt.Errorf("dapr metadata 解析失败：%w", err)
	}
	return m.AppConnectionProperties, nil
}

// StartWorkflow 启动一个 workflow 实例。instanceID 相同时 Dapr 返回 409，
// 这正是「同一 backlog 不许并发跑」的锁：调用方据此判定重复发起。
func (c *Client) StartWorkflow(ctx context.Context, component, workflow, instanceID string, input any) error {
	u := fmt.Sprintf("%s/v1.0/workflows/%s/%s/start?instanceID=%s",
		c.base, url.PathEscape(component), url.PathEscape(workflow), url.QueryEscape(instanceID))
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("dapr start workflow %s: %d %s", instanceID, resp.StatusCode, respBody)
	}
	return nil
}

// RaiseEvent 把人的决定送回挂起的 workflow。
// 注意：payload 只是「事实 ID + 决定」，不是权限凭据——
// 谁有权决定，在 BFF 侧已经判定过了。
func (c *Client) RaiseEvent(ctx context.Context, component, instanceID, event string, payload any) error {
	u := fmt.Sprintf("%s/v1.0/workflows/%s/%s/raiseEvent/%s",
		c.base, url.PathEscape(component), url.PathEscape(instanceID), url.PathEscape(event))
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("dapr raise event %s: %d %s", instanceID, resp.StatusCode, b)
	}
	return nil
}

// WorkflowStatus 是 get workflow 返回里我们关心的字段。
type WorkflowStatus struct {
	InstanceID    string `json:"instanceID"`
	RuntimeStatus string `json:"runtimeStatus"`
	Properties    struct {
		DaprWorkflowVersion string          `json:"dapr.workflow.version"`
		DaprWorkflowInput   json.RawMessage `json:"dapr.workflow.input"`
	} `json:"properties"`
}

// GetWorkflow 查询实例状态。实例不存在返回 (nil, nil)。
func (c *Client) GetWorkflow(ctx context.Context, component, instanceID string) (*WorkflowStatus, error) {
	u := fmt.Sprintf("%s/v1.0/workflows/%s/%s", c.base, url.PathEscape(component), url.PathEscape(instanceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("dapr get workflow %s: %d %s", instanceID, resp.StatusCode, body)
	}
	var st WorkflowStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// TerminateWorkflow 终止实例（用于「退回修改」）。
func (c *Client) TerminateWorkflow(ctx context.Context, component, instanceID string) error {
	u := fmt.Sprintf("%s/v1.0/workflows/%s/%s/terminate",
		c.base, url.PathEscape(component), url.PathEscape(instanceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("dapr terminate %s: %d %s", instanceID, resp.StatusCode, b)
	}
	return nil
}
