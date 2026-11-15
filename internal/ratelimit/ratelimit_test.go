package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(3, time.Minute)
	l.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allowed("k") {
			t.Fatal("blocked early")
		}
		l.Fail("k")
	}
	if l.Allowed("k") {
		t.Fatal("not blocked")
	}
	now = now.Add(time.Minute + time.Second)
	if !l.Allowed("k") {
		t.Fatal("window did not slide")
	}
	l.Fail("k")
	l.Reset("k")
	if !l.Allowed("k") {
		t.Fatal("reset")
	}
}
