package clock

import (
	"sync"
	"testing"
	"time"
)

func TestSystem(t *testing.T) {
	before := time.Now()
	got := System{}.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("System.Now() = %v, outside [%v, %v]", got, before, after)
	}
}

func TestFake(t *testing.T) {
	start := time.Date(2026, 10, 27, 9, 15, 0, 0, time.UTC)
	f := NewFake(start)
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	f.Advance(5 * time.Minute)
	want := start.Add(5 * time.Minute)
	if got := f.Now(); !got.Equal(want) {
		t.Fatalf("after Advance = %v, want %v", got, want)
	}

	f.Set(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	if got := f.Now(); got.Year() != 2030 {
		t.Fatalf("after Set = %v", got)
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				f.Advance(time.Millisecond)
				_ = f.Now()
			}
		}()
	}
	wg.Wait()
	want := time.Unix(0, 0).Add(2000 * time.Millisecond)
	if got := f.Now(); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}

func TestClockInterface(t *testing.T) {
	var c Clock = System{}
	if c.Now().IsZero() {
		t.Fatal("system clock returned zero time")
	}
	c = NewFake(time.Unix(1, 0))
	if !c.Now().Equal(time.Unix(1, 0)) {
		t.Fatalf("fake clock = %v", c.Now())
	}
}
