package dapr

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Unreachable 只认「连不上 / 暂时不可用」；业务错误（etag 冲突、参数错）不能被它吞成重试。
func TestUnreachableOnlyForTransportAndTransient(t *testing.T) {
	c := New("http://127.0.0.1:1") // 没有进程监听这个端口：连不上
	_, _, err := c.GetState(context.Background(), "statestore", "k")
	if err == nil || !Unreachable(err) {
		t.Fatalf("连不上 sidecar 应判为可重试，err=%v", err)
	}

	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{ErrConflict, false},
		{errors.New("dapr set state statestore/x: 400 bad request"), false},
		{errors.New("dapr get state statestore/x: 503 service unavailable"), true},
		{fmt.Errorf("wrap: %w", context.DeadlineExceeded), true},
	} {
		if got := Unreachable(tc.err); got != tc.want {
			t.Errorf("Unreachable(%v) = %v，期望 %v", tc.err, got, tc.want)
		}
	}
}
