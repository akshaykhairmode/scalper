package market

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeOIBaselineStore struct {
	mu    sync.Mutex
	byDay map[string]map[string]int64
	saves int
}

func newFakeOIBaselineStore() *fakeOIBaselineStore {
	return &fakeOIBaselineStore{byDay: make(map[string]map[string]int64)}
}

func (f *fakeOIBaselineStore) LoadOIBaselines(_ context.Context, day string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64)
	for key, value := range f.byDay[day] {
		out[key] = value
	}
	return out, nil
}

func (f *fakeOIBaselineStore) SaveOIBaselines(_ context.Context, day string, baselines map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.byDay[day] == nil {
		f.byDay[day] = make(map[string]int64)
	}
	for key, value := range baselines {
		// Same contract as the SQL upsert: first write wins.
		if _, exists := f.byDay[day][key]; !exists {
			f.byDay[day][key] = value
		}
	}
	return nil
}

func newTestOIService(store OIBaselineStore) *Service {
	service := &Service{
		log:    slog.Default(),
		oiBase: make(map[string]int64),
	}
	if store != nil {
		service.oiStore = store
	}
	return service
}

func TestOIChangeFromFirstObservation(t *testing.T) {
	ctx := context.Background()
	service := newTestOIService(nil)
	day := "2026-11-18"

	change, pct := service.oiChangeForDay(ctx, "nse_fo|25000CE", 100000, day)
	if change != 0 || pct != 0 {
		t.Fatalf("first observation = (%d, %v), want (0, 0)", change, pct)
	}

	change, pct = service.oiChangeForDay(ctx, "nse_fo|25000CE", 125000, day)
	if change != 25000 {
		t.Fatalf("change = %d, want 25000", change)
	}
	if pct < 24.99 || pct > 25.01 {
		t.Fatalf("pct = %v, want 25", pct)
	}

	change, pct = service.oiChangeForDay(ctx, "nse_fo|25000CE", 90000, day)
	if change != -10000 {
		t.Fatalf("change = %d, want -10000", change)
	}
	if pct < -10.01 || pct > -9.99 {
		t.Fatalf("pct = %v, want -10", pct)
	}
}

func TestOIChangeResetsOnNewISTDay(t *testing.T) {
	ctx := context.Background()
	service := newTestOIService(nil)
	service.oiChangeForDay(ctx, "nse_fo|25000CE", 100000, "2026-11-18")
	if change, _ := service.oiChangeForDay(ctx, "nse_fo|25000CE", 140000, "2026-11-18"); change != 40000 {
		t.Fatalf("same-day change = %d, want 40000", change)
	}

	change, pct := service.oiChangeForDay(ctx, "nse_fo|25000CE", 140000, "2026-11-19")
	if change != 0 || pct != 0 {
		t.Fatalf("new-day first observation = (%d, %v), want (0, 0)", change, pct)
	}
	if change, _ := service.oiChangeForDay(ctx, "nse_fo|25000CE", 150000, "2026-11-19"); change != 10000 {
		t.Fatalf("new-day change = %d, want 10000", change)
	}
}

func TestOIChangeSurvivesRestartWithPersistedBaselines(t *testing.T) {
	ctx := context.Background()
	store := newFakeOIBaselineStore()
	day := "2026-11-18"

	before := newTestOIService(store)
	before.oiChangeForDay(ctx, "nse_fo|25000CE", 100000, day)
	before.flushOIBaselines(ctx)
	if store.saves == 0 {
		t.Fatal("expected the seeded baseline to be persisted")
	}

	// A new Service models a restart: memory is empty, the store is not.
	after := newTestOIService(store)
	change, pct := after.oiChangeForDay(ctx, "nse_fo|25000CE", 120000, day)
	if change != 20000 {
		t.Fatalf("post-restart change = %d, want 20000", change)
	}
	if pct < 19.99 || pct > 20.01 {
		t.Fatalf("post-restart pct = %v, want 20", pct)
	}
}

func TestOIChangeLoadFailureRetriesAndKeepsSeeding(t *testing.T) {
	ctx := context.Background()
	store := &flakyOIBaselineStore{fakeOIBaselineStore: newFakeOIBaselineStore()}
	service := newTestOIService(store)
	day := "2026-11-18"

	// First load fails: the call still seeds in memory and reports zero.
	if change, _ := service.oiChangeForDay(ctx, "nse_fo|25000CE", 100000, day); change != 0 {
		t.Fatalf("change = %d, want 0", change)
	}
	service.flushOIBaselines(ctx)
	// Next call reloads; the persisted baseline matches the seeded value.
	if change, _ := service.oiChangeForDay(ctx, "nse_fo|25000CE", 110000, day); change != 10000 {
		t.Fatalf("change after retry = %d, want 10000", change)
	}
}

type flakyOIBaselineStore struct {
	*fakeOIBaselineStore
	loads int
}

func (f *flakyOIBaselineStore) LoadOIBaselines(ctx context.Context, day string) (map[string]int64, error) {
	f.loads++
	if f.loads == 1 {
		return nil, context.DeadlineExceeded
	}
	return f.fakeOIBaselineStore.LoadOIBaselines(ctx, day)
}

func TestOIChangeIgnoresMissingQuotesAndIsConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	service := newTestOIService(nil)
	day := time.Date(2026, time.November, 18, 10, 0, 0, 0, istZone).Format(oiDayLayout)

	if change, pct := service.oiChangeForDay(ctx, "", 100, day); change != 0 || pct != 0 {
		t.Fatalf("empty key = (%d, %v), want (0, 0)", change, pct)
	}
	if change, pct := service.oiChangeForDay(ctx, "nse_fo|25000CE", 0, day); change != 0 || pct != 0 {
		t.Fatalf("zero OI = (%d, %v), want (0, 0)", change, pct)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "nse_fo|25000CE"
			if i%2 == 0 {
				key = "nse_fo|25000PE"
			}
			service.oiChangeForDay(ctx, key, int64(1000+i), day)
		}(i)
	}
	wg.Wait()
}

type failingSaveStore struct {
	*fakeOIBaselineStore
	mu     sync.Mutex
	fail   bool
	onSave func()
}

func (f *failingSaveStore) SaveOIBaselines(ctx context.Context, day string, baselines map[string]int64) error {
	f.mu.Lock()
	fail := f.fail
	hook := f.onSave
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if fail {
		return errors.New("baseline store unavailable")
	}
	return f.fakeOIBaselineStore.SaveOIBaselines(ctx, day, baselines)
}

func TestOIBaselineSaveFailureRequeuesForNextChainCall(t *testing.T) {
	ctx := context.Background()
	store := &failingSaveStore{fakeOIBaselineStore: newFakeOIBaselineStore(), fail: true}
	service := newTestOIService(store)
	day := "2026-11-18"
	key := "nse_fo|25000CE"

	service.oiChangeForDay(ctx, key, 1000, day)
	service.flushOIBaselines(ctx)

	service.oiMu.Lock()
	pending := service.oiPending[key]
	service.oiMu.Unlock()
	if pending != 1000 {
		t.Fatalf("pending baseline = %d, want 1000 after a failed save", pending)
	}

	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	service.flushOIBaselines(ctx)

	loaded, err := store.LoadOIBaselines(ctx, day)
	if err != nil {
		t.Fatalf("LoadOIBaselines: %v", err)
	}
	if loaded[key] != 1000 {
		t.Fatalf("persisted baseline = %d, want 1000", loaded[key])
	}
}

func TestOIBaselineRequeueDoesNotCrossTradingDays(t *testing.T) {
	ctx := context.Background()
	store := &failingSaveStore{fakeOIBaselineStore: newFakeOIBaselineStore(), fail: true}
	service := newTestOIService(store)
	oldDay := "2026-11-18"

	service.oiChangeForDay(ctx, "nse_fo|25000CE", 1000, oldDay)
	// Simulate the IST day rolling over while the failed write is in flight.
	store.onSave = func() {
		service.oiMu.Lock()
		service.oiDay = "2026-11-19"
		service.oiMu.Unlock()
	}
	service.flushOIBaselines(ctx)

	service.oiMu.Lock()
	pending := len(service.oiPending)
	service.oiMu.Unlock()
	if pending != 0 {
		t.Fatalf("cross-day requeue left %d pending baselines, want 0", pending)
	}
}
