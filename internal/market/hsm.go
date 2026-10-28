// Package market provides Kotak REST quotes and SFeed market data.
package market

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/hsm"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// HSMEvent reports feed status changes and market updates.
type HSMEvent struct {
	Status string
	Quote  *hsm.Quote
}

type hsmSubscription struct {
	token string
	index bool
}

// HSMFeed manages the authenticated Kotak SFeed connection.
type HSMFeed struct {
	cfg    config.Config
	scrips *scrip.Manager
	log    *slog.Logger

	mu      sync.RWMutex
	client  *hsm.HSMClient
	user    string
	auth    string
	feedURL string
	subs    map[string]hsmSubscription
	events  chan HSMEvent
	cancel  func()
	done    chan struct{}
}

// NewHSMFeed creates a market feed using the configured Scrip Master.
func NewHSMFeed(cfg config.Config, scripts *scrip.Manager, logger *slog.Logger) *HSMFeed {
	if logger == nil {
		logger = slog.Default()
	}
	return &HSMFeed{
		cfg:    cfg,
		scrips: scripts,
		log:    logger,
		subs:   make(map[string]hsmSubscription),
		events: make(chan HSMEvent, 512),
	}
}

// Start authenticates and starts the SFeed reconnect loop.
func (f *HSMFeed) Start(user, auth, feedURL string) error {
	if strings.TrimSpace(user) == "" || strings.TrimSpace(auth) == "" {
		return errors.New("SFeed user and auth values are required")
	}
	f.mu.Lock()
	if f.cancel != nil {
		f.mu.Unlock()
		return errors.New("HSM feed is already started")
	}
	f.user, f.auth = strings.TrimSpace(user), strings.TrimSpace(auth)
	f.feedURL = strings.TrimSpace(feedURL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	f.cancel = cancel
	f.done = done
	f.mu.Unlock()
	go func() {
		defer close(done)
		f.connectionLoop(ctx)
	}()
	return nil
}

// Stop closes the feed and waits for its reconnect loop to exit.
func (f *HSMFeed) Stop() {
	f.mu.Lock()
	client := f.client
	cancel := f.cancel
	done := f.done
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		client.Stop()
	}
	if done != nil {
		<-done
	}
	f.mu.Lock()
	if f.done == done {
		f.client = nil
		f.user, f.auth, f.feedURL = "", "", ""
		f.cancel = nil
		f.done = nil
	}
	f.mu.Unlock()
	f.emit(HSMEvent{Status: "polling"})
}

// Events returns the feed status and market update stream.
func (f *HSMFeed) Events() <-chan HSMEvent {
	return f.events
}

// IsConnected reports whether the SFeed socket is authenticated.
func (f *HSMFeed) IsConnected() bool {
	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()
	return client != nil && client.IsConnected()
}

// Subscribe adds instrument keys to the feed subscription set.
func (f *HSMFeed) Subscribe(keys []string) error {
	var scripTokens, indexTokens []string
	f.mu.Lock()
	client := f.client
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		subscription, err := f.topicForKey(key)
		if err != nil {
			f.mu.Unlock()
			return err
		}
		if _, exists := f.subs[key]; exists {
			continue
		}
		f.subs[key] = subscription
		if subscription.index {
			indexTokens = append(indexTokens, subscription.token)
		} else {
			scripTokens = append(scripTokens, subscription.token)
		}
	}
	f.mu.Unlock()
	if client != nil && client.IsConnected() {
		if len(indexTokens) > 0 {
			if err := client.SubscribeIndices(indexTokens); err != nil {
				return err
			}
		}
		if len(scripTokens) > 0 {
			if err := client.SubscribeScrips(scripTokens); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unsubscribe removes instrument keys from the feed subscription set.
func (f *HSMFeed) Unsubscribe(keys []string) error {
	var scripTokens, indexTokens []string
	f.mu.Lock()
	client := f.client
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if subscription, ok := f.subs[key]; ok {
			if subscription.index {
				indexTokens = append(indexTokens, subscription.token)
			} else {
				scripTokens = append(scripTokens, subscription.token)
			}
			delete(f.subs, key)
		}
	}
	f.mu.Unlock()
	if client != nil && client.IsConnected() {
		if len(indexTokens) > 0 {
			if err := client.UnsubscribeIndices(indexTokens); err != nil {
				return err
			}
		}
		if len(scripTokens) > 0 {
			if err := client.UnsubscribeScrips(scripTokens); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *HSMFeed) connectionLoop(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		quotes := make(chan *hsm.Quote, 256)
		client := hsm.NewHSMClientWithLogger(f.feedEndpoint(), quotes, f.log)
		f.mu.RLock()
		user, auth := f.user, f.auth
		f.mu.RUnlock()
		connectErr := client.Connect(ctx, user, auth)
		if connectErr != nil {
			client.Stop()
			f.emit(HSMEvent{Status: "degraded"})
			f.log.Error("Kotak HSM connection failed", "event", "hsm_connect_failed",
				"url", f.cfg.Market.HSMURL, "error", connectErr)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		f.mu.Lock()
		if ctx.Err() != nil {
			f.mu.Unlock()
			client.Stop()
			return
		}
		f.client = client
		indexTokens := make([]string, 0, len(f.cfg.Market.Indices))
		scripTokens := make([]string, 0, len(f.subs))
		for _, index := range f.cfg.Market.Indices {
			indexTokens = append(indexTokens, index.IndexSegment+"|"+IndexQuoteSymbol(index))
		}
		for _, subscription := range f.subs {
			if subscription.index {
				indexTokens = append(indexTokens, subscription.token)
			} else {
				scripTokens = append(scripTokens, subscription.token)
			}
		}
		f.mu.Unlock()
		if err := client.SubscribeIndices(indexTokens); err != nil {
			f.log.Error("Kotak SFeed index subscription failed", "event", "sfeed_subscribe_failed", "error", err)
			f.emit(HSMEvent{Status: "degraded"})
			f.clearClient(client)
			client.Stop()
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		if err := client.SubscribeScrips(scripTokens); err != nil {
			f.log.Error("Kotak HSM initial subscription failed", "event", "hsm_subscribe_failed", "error", err)
			f.emit(HSMEvent{Status: "degraded"})
			f.clearClient(client)
			client.Stop()
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		f.emit(HSMEvent{Status: "live"})
		f.log.Info("Kotak HSM subscriptions active",
			"event", "sfeed_subscribed", "index_count", len(indexTokens), "scrip_count", len(scripTokens))
		disconnected := false
		for !disconnected {
			select {
			case quote := <-quotes:
				f.emit(HSMEvent{Quote: quote})
			case err, ok := <-client.Done():
				if ok && err != nil {
					f.log.Warn("Kotak HSM stream closed", "event", "hsm_stream_closed", "error", err)
				}
				disconnected = true
			case <-ctx.Done():
				client.Stop()
				f.clearClient(client)
				return
			}
		}
		f.clearClient(client)
		client.Stop()
		f.emit(HSMEvent{Status: "degraded"})
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (f *HSMFeed) emit(event HSMEvent) {
	select {
	case f.events <- event:
	default:
		f.log.Warn("dropping HSM event because feed queue is full", "event", "hsm_event_dropped")
	}
}

func (f *HSMFeed) clearClient(client *hsm.HSMClient) {
	f.mu.Lock()
	if f.client == client {
		f.client = nil
	}
	f.mu.Unlock()
}

func (f *HSMFeed) topicForKey(key string) (hsmSubscription, error) {
	if f.scrips != nil {
		if option, ok := f.scrips.Option(key); ok {
			return hsmSubscription{token: option.ExchangeSegment + "|" + option.PSymbol}, nil
		}
		if spot, ok := f.scrips.Spot(key); ok && spot.ExchangeSegment == scrip.SegmentMCXFO {
			return hsmSubscription{token: spot.ExchangeSegment + "|" + spot.PSymbol}, nil
		}
	}
	for _, index := range f.cfg.Market.Indices {
		spotKey := index.IndexSegment + "|" + index.ScripName
		if key == spotKey {
			return hsmSubscription{
				token: index.IndexSegment + "|" + IndexQuoteSymbol(index),
				index: true,
			}, nil
		}
	}
	return hsmSubscription{}, errors.New("instrument is not available for HSM subscription")
}

func (f *HSMFeed) feedEndpoint() string {
	f.mu.RLock()
	sessionURL := f.feedURL
	f.mu.RUnlock()
	if configured := strings.TrimSpace(f.cfg.Market.HSMURL); configured != "" {
		return configured
	}
	if sessionURL != "" {
		return sessionURL
	}
	return hsm.DefaultURL
}
