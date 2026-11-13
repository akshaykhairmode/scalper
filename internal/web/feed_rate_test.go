package web

import (
	"testing"
	"time"
)

func TestFeedClientSubscribeRateLimit(t *testing.T) {
	c := &feedClient{}
	now := time.Now()
	for i := 0; i < maxFeedCommandsPerMinute; i++ {
		if !c.allowSubscribe(now) {
			t.Fatalf("command %d should be allowed", i)
		}
	}
	if c.allowSubscribe(now) {
		t.Fatal("expected the next command to be rate limited")
	}
	if !c.allowUnsubscribe(now) {
		t.Fatal("unsubscribe should have its own bucket")
	}
	if !c.allowSubscribe(now.Add(2 * time.Minute)) {
		t.Fatal("rate limit window should reset")
	}
}
