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

type HSMEvent struct {
	Status string
	Quote  *hsm.Quote
}

type HSMFeed struct {
	cfg    config.Config
	scrips *scrip.Manager
	log    *slog.Logger

	mu     sync.RWMutex
	client *hsm.HSMClient
	token  string
	sid    string
	subs   map[string]string
	events chan HSMEvent
	cancel func()
	done   chan struct{}
}

func NewHSMFeed(cfg config.Config, scripts *scrip.Manager, logger *slog.Logger) *HSMFeed {
	if logger == nil {
		logger = slog.Default()
	}
	return &HSMFeed{
		cfg:    cfg,
		scrips: scripts,
		log:    logger,
		subs:   make(map[string]string),
		events: make(chan HSMEvent, 512),
	}
}

func (f *HSMFeed) Start(token, sid string) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(sid) == "" {
		return errors.New("HSM session token and SID are required")
	}
	f.mu.Lock()
	if f.cancel != nil {
		f.mu.Unlock()
		return errors.New("HSM feed is already started")
	}
	f.token, f.sid = token, sid
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
		f.token, f.sid = "", ""
		f.cancel = nil
		f.done = nil
	}
	f.mu.Unlock()
	f.emit(HSMEvent{Status: "polling"})
}

func (f *HSMFeed) Events() <-chan HSMEvent {
	return f.events
}

func (f *HSMFeed) IsConnected() bool {
	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()
	return client != nil && client.IsConnected()
}

func (f *HSMFeed) Subscribe(keys []string) error {
	var subscribe []string
	f.mu.Lock()
	client := f.client
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		topic, err := f.topicForKey(key)
		if err != nil {
			f.mu.Unlock()
			return err
		}
		if _, exists := f.subs[key]; exists {
			continue
		}
		f.subs[key] = topic
		subscribe = append(subscribe, topic)
	}
	f.mu.Unlock()
	if len(subscribe) > 0 && client != nil && client.IsConnected() {
		if err := client.Subscribe(subscribe); err != nil {
			return err
		}
	}
	return nil
}

func (f *HSMFeed) Unsubscribe(keys []string) error {
	var unsubscribe []string
	f.mu.Lock()
	client := f.client
	for _, key := range keys {
		if topic, ok := f.subs[key]; ok {
			unsubscribe = append(unsubscribe, topic)
			delete(f.subs, key)
		}
	}
	f.mu.Unlock()
	if len(unsubscribe) > 0 && client != nil && client.IsConnected() {
		if err := client.Unsubscribe(unsubscribe); err != nil {
			return err
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
		client := hsm.NewHSMClientWithLogger(f.cfg.Market.HSMURL, quotes, f.log)
		f.mu.RLock()
		token, sid := f.token, f.sid
		f.mu.RUnlock()
		connectErr := client.Connect(ctx, token, sid)
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
		topics := make([]string, 0, len(f.subs)+len(f.cfg.Market.Indices))
		seen := make(map[string]bool)
		for _, index := range f.cfg.Market.Indices {
			topic := "if|" + index.IndexSegment + "|" + index.IndexSymbol
			if !seen[topic] {
				topics = append(topics, topic)
				seen[topic] = true
			}
		}
		for _, topic := range f.subs {
			if !seen[topic] {
				topics = append(topics, topic)
				seen[topic] = true
			}
		}
		f.mu.Unlock()
		if err := client.Subscribe(topics); err != nil {
			f.log.Error("Kotak HSM initial subscription failed", "event", "hsm_subscribe_failed", "error", err)
			f.emit(HSMEvent{Status: "degraded"})
			f.clearClient(client)
			client.Stop()
			continue
		}
		backoff = time.Second
		f.emit(HSMEvent{Status: "live"})
		f.log.Info("Kotak HSM subscriptions active",
			"event", "hsm_subscribed", "count", len(topics))
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

func (f *HSMFeed) topicForKey(key string) (string, error) {
	if option, ok := f.scrips.Option(key); ok {
		return "sf|" + option.ExchangeSegment + "|" + option.PSymbol, nil
	}
	for _, index := range f.cfg.Market.Indices {
		spotKey := index.IndexSegment + "|" + index.ScripName
		if key == spotKey {
			return "if|" + index.IndexSegment + "|" + index.IndexSymbol, nil
		}
	}
	return "", errors.New("instrument is not available for HSM subscription")
}
