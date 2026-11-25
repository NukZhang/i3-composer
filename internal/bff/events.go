package bff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhangkun/i3-composer/internal/store"
)

// 本文件是 Dapr pub/sub 的「接收端」。
//
// 以前只有发：workflow 把 tag.created 发进 Redis stream，谁都不消费，
// 消息就只是堆积着。现在补上收：
//
//   - GET  /dapr/subscribe        声明订阅哪些主题、由哪个路由收
//   - POST /api/events/{topic}    Dapr 把 CloudEvent 投递到这里
//
// 收到的事件会落一条 event.received 事实（记在 events 日志里），
// 并推给 SSE 订阅者。于是「事件外发」这件事有了闭环：
// 事实 → 总线 → 消费 → 事实 → 页面。

// EventTopic 是一个订阅：主题名 + 收它的路由 + 落到哪个账本。
type EventTopic struct {
	Topic string
	Route string
	// PBC 是这条事件归属的交付单元；空表示全局事件。
	PBC string
}

// eventTopics 是 BFF 订阅的全部主题。
//
// 主题名是契约：发端（workflow / handler）与收端共用同一份定义。
var eventTopics = []EventTopic{
	{Topic: "tag.created", Route: "/api/events/tag.created"},
	{Topic: "item.changed", Route: "/api/events/item.changed"},
	{Topic: "agent.changed", Route: "/api/events/agent.changed"},
}

// eventsLog 是事件收件的账本。事件不是业务事实，单独一条日志，
// 免得和交付单元的事实混在一起。
const eventsLog = "events"

// daprSubscribe 是 Dapr 的订阅声明端点。
//
// Dapr sidecar 启动后会来问这个端点：我要订阅哪些主题、投递到哪。
// 返回空数组就是不订阅。
func (s *Server) daprSubscribe(w http.ResponseWriter, r *http.Request) {
	type sub struct {
		Pubsubname string `json:"pubsubname"`
		Topic      string `json:"topic"`
		Route      string `json:"route"`
	}
	out := make([]sub, 0, len(eventTopics))
	for _, t := range eventTopics {
		out = append(out, sub{Pubsubname: "pubsub", Topic: t.Topic, Route: t.Route})
	}
	writeJSON(w, http.StatusOK, out)
}

// receiveEvent 收一个 CloudEvent。
//
// 它做三件事：把事件落到 events 账本、推 SSE、回答 Dapr「收到了」。
// 投递失败要返回 5xx——Dapr 会重投，吞掉就等于丢事件。
func (s *Server) receiveEvent(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	topic := r.PathValue("topic")
	// 只收声明过的主题。这个路由是 Dapr 的回调口，不对路径做校验的话，
	// 任何 topic 字符串都能往账本里写一条「收到的事件」。
	if _, ok := eventTopicOf(topic); !ok {
		return fmt.Errorf("%w：未订阅的主题 %s", errNotFound, topic)
	}
	var body struct {
		ID      string          `json:"id"`
		Source  string          `json:"source"`
		Type    string          `json:"type"`
		Subject string          `json:"subject"`
		Time    string          `json:"time"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		return fmt.Errorf("%w：事件体不合法：%v", errBadRequest, err)
	}
	var data map[string]any
	if len(body.Data) > 0 {
		_ = json.Unmarshal(body.Data, &data)
	}
	pbc := ""
	if data != nil {
		if v, ok := data["pbc"].(string); ok {
			pbc = v
		}
	}
	rec := map[string]any{
		"topic":   topic,
		"eventId": body.ID,
		"source":  body.Source,
		"subject": body.Subject,
		"at":      time.Now().Format("01-02 15:04:05"),
		"pbc":     pbc,
		"data":    data,
	}
	if _, err := s.Log.Append(r.Context(), eventsLog, store.FactEventReceived, "dapr", rec); err != nil {
		return err
	}
	// 收到的事件也要能推到页面：推给归属单元（没有就推给所有订阅者）。
	s.publish(pbc, "event.received", rec)
	writeJSON(w, http.StatusOK, map[string]any{"received": true, "topic": topic})
	return nil
}

// receivedEvents 列出已收的事件（页面上的「事件流」）。
func (s *Server) receivedEvents(w http.ResponseWriter, r *http.Request, c reqCtx) error {
	facts, err := s.Log.Load(r.Context(), eventsLog)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(facts))
	for i := len(facts) - 1; i >= 0; i-- {
		f := facts[i]
		if f.Type != store.FactEventReceived {
			continue
		}
		var p map[string]any
		_ = json.Unmarshal(f.Payload, &p)
		out = append(out, map[string]any{
			"seq": f.Seq, "at": f.At, "actor": f.Actor, "payload": p,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
	return nil
}

// eventsFor 取某个交付单元收到的事件。
//
// 事件存在 events 日志里（跨单元共享），这里按归属单元过滤。
func (s *Server) eventsFor(ctx context.Context, pbc string) []store.EventRecord {
	facts, err := s.Log.Load(ctx, eventsLog)
	if err != nil {
		return nil
	}
	out := make([]store.EventRecord, 0, 8)
	for i := len(facts) - 1; i >= 0; i-- {
		f := facts[i]
		if f.Type != store.FactEventReceived {
			continue
		}
		var p struct {
			Topic   string         `json:"topic"`
			EventID string         `json:"eventId"`
			At      string         `json:"at"`
			PBC     string         `json:"pbc"`
			Data    map[string]any `json:"data"`
		}
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			continue
		}
		if pbc != "" && p.PBC != "" && p.PBC != pbc {
			continue
		}
		out = append(out, store.EventRecord{
			Topic: p.Topic, EventID: p.EventID, At: p.At, PBC: p.PBC, Data: p.Data,
		})
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// eventTopicOf 找一个主题的定义。
func eventTopicOf(topic string) (EventTopic, bool) {
	for _, t := range eventTopics {
		if strings.EqualFold(t.Topic, topic) {
			return t, true
		}
	}
	return EventTopic{}, false
}
