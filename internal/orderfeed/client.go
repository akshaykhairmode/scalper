// Package orderfeed implements Kotak Neo's order and position WebSocket feed.
package orderfeed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Session contains the authenticated credentials needed to connect to the feed.
type Session struct{ BaseURL, SID, Token string }

// Event is a normalized event or connection status from the order feed.
type Event struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data,omitempty"`
	Status string          `json:"status,omitempty"`
}

// Client maintains a reconnecting Kotak order and position feed.
type Client struct {
	log       *slog.Logger
	mu        sync.Mutex
	cancel    context.CancelFunc
	events    chan Event
	connected bool
}

// New creates a feed client.
func New(log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{log: log, events: make(chan Event, 256)}
}

// Events returns the channel carrying feed events and status changes.
func (c *Client) Events() <-chan Event { return c.events }

// Connected reports whether the feed currently has an active connection.
func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }

// Start begins connecting and reconnecting using the authenticated session.
func (c *Client) Start(parent context.Context, s Session) error {
	if s.SID == "" || s.Token == "" {
		return errors.New("kotak order feed requires an authenticated session")
	}
	u, err := feedURL(s.BaseURL)
	if err != nil {
		return err
	}
	c.Stop()
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go c.run(ctx, u, s)
	return nil
}

// Stop cancels the active connection and its reconnect loop.
func (c *Client) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (c *Client) run(ctx context.Context, endpoint string, s Session) {
	delay := time.Second
	for ctx.Err() == nil {
		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
		if resp != nil && resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil && c.log != nil {
				c.log.Debug("failed to close order feed handshake response body", "error", cerr)
			}
		}
		if err == nil {
			err = c.readConnection(ctx, conn, s)
		}
		c.setConnected(false)
		c.emit(Event{Status: "disconnected"})
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("Kotak order feed disconnected", "event", "order_feed_disconnected", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}
func (c *Client) readConnection(ctx context.Context, conn *websocket.Conn, s Session) error {
	defer func() {
		if cerr := conn.Close(); cerr != nil && c.log != nil {
			c.log.Debug("failed to close order feed connection", "error", cerr)
		}
	}()
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return err
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	login := fmt.Sprintf("{type:cn,Authorization:%s,Sid:%s,src:WEB}", s.Token, s.SID)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(login)); err != nil {
		return err
	}
	c.setConnected(true)
	c.emit(Event{Status: "live"})
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	done := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			var event Event
			if json.Unmarshal(raw, &event) != nil || event.Type == "" {
				continue
			}
			if event.Type == "cn" || event.Type == "ak" {
				continue
			}
			if event.Type == "order" || event.Type == "position" {
				c.emit(event)
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil && c.log != nil {
				c.log.Debug("failed to send close message", "error", err)
			}
			return ctx.Err()
		case err := <-done:
			return err
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return err
			}
		}
	}
}
func (c *Client) setConnected(v bool) { c.mu.Lock(); c.connected = v; c.mu.Unlock() }
func (c *Client) emit(e Event) {
	select {
	case c.events <- e:
	default:
		c.log.Warn("Kotak order feed event dropped", "event", "order_feed_event_dropped", "type", e.Type)
	}
}
func feedURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/realtime")
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", errors.New("invalid Kotak order feed base URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "kotaksecurities.com" && !strings.HasSuffix(host, ".kotaksecurities.com") {
		return "", errors.New("untrusted Kotak order feed base URL")
	}
	u.Path = "/realtime"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.Scheme = "wss"
	return u.String(), nil
}
