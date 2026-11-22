// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Package webhooks implements per-project outbound webhook subscriptions
// for Kryton's CloudEvents: HMAC-signed deliveries with exponential
// backoff retries, a bounded delivery log, a dead-letter list for
// deliveries that exhausted their retries, and an SSRF guard.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/id"
)

// Delivery statuses.
const (
	StatusPending   = "pending"
	StatusRetrying  = "retrying"
	StatusSucceeded = "succeeded"
	StatusDead      = "dead"
)

// Limits.
const (
	MaxSubscriptionsPerProject = 50
	maxAttemptLog              = 10
	maxResponseBytes           = 2048
)

// DefaultBackoff is the wait before attempt 2, 3, ... (so six attempts total).
var DefaultBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// ErrNotFound is returned for unknown subscription or delivery ids.
var ErrNotFound = errors.New("not found")

// ErrLimit is returned when a project already has the maximum subscriptions.
var ErrLimit = errors.New("subscription limit reached for project")

// Subscription is one project's outbound webhook.
type Subscription struct {
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	EventTypes  []string  `json:"eventTypes"` // glob patterns; empty/"*" = all
	Enabled     bool      `json:"enabled"`
	Secret      string    `json:"secret,omitempty"` // only returned at creation
	CreatedBy   string    `json:"createdBy,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Public returns a copy with the signing secret removed.
func (s Subscription) Public() Subscription { s.Secret = ""; return s }

// Attempt is one delivery try in a delivery's log.
type Attempt struct {
	Time       time.Time `json:"time"`
	StatusCode int       `json:"statusCode,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMs int64     `json:"durationMs"`
}

// Delivery tracks one event bound for one subscription.
type Delivery struct {
	ID             string          `json:"id"`
	SubscriptionID string          `json:"subscriptionId"`
	Project        string          `json:"project"`
	EventID        string          `json:"eventId"`
	EventType      string          `json:"eventType"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	LastStatusCode int             `json:"lastStatusCode,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	NextAttemptAt  *time.Time      `json:"nextAttemptAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	AttemptLog     []Attempt       `json:"attemptLog,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

// WithoutPayload returns a copy suitable for list responses.
func (d Delivery) WithoutPayload() Delivery { d.Payload = nil; d.AttemptLog = nil; return d }

// Config configures a Manager.
type Config struct {
	// File persists subscriptions; the delivery log is stored beside it
	// (<name>-deliveries.json). Empty means memory only.
	File          string
	Guard         *Guard
	Client        *http.Client // override (tests)
	Backoff       []time.Duration
	Workers       int
	Tick          time.Duration
	MaxDeliveries int
	Now           func() time.Time
	Log           *slog.Logger
	// OnAttempt is called after every delivery attempt (metrics hook).
	OnAttempt func(project, outcome string)
}

// Manager owns subscriptions, the delivery log and the delivery workers.
type Manager struct {
	cfg     Config
	mu      sync.Mutex
	subs    map[string]*Subscription
	deliv   map[string]*Delivery
	order   []string // delivery ids, oldest first
	busy    map[string]bool
	dirty   bool
	queue   chan string
	client  *http.Client
	started bool
}

// NewManager loads persisted subscriptions and deliveries.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Guard == nil {
		g, _ := NewGuard(nil)
		cfg.Guard = g
	}
	if len(cfg.Backoff) == 0 {
		cfg.Backoff = DefaultBackoff
	}
	if cfg.Workers < 1 {
		cfg.Workers = 4
	}
	if cfg.Tick <= 0 {
		cfg.Tick = time.Second
	}
	if cfg.MaxDeliveries < 10 {
		cfg.MaxDeliveries = 500
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m := &Manager{cfg: cfg, subs: map[string]*Subscription{}, deliv: map[string]*Delivery{}, busy: map[string]bool{}, queue: make(chan string, 1024)}
	m.client = cfg.Client
	if m.client == nil {
		m.client = cfg.Guard.NewClient(10 * time.Second)
	}
	if cfg.File != "" {
		if err := m.load(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) deliveriesPath() string {
	return strings.TrimSuffix(m.cfg.File, ".json") + "-deliveries.json"
}

func (m *Manager) load() error {
	// #nosec G304 -- operator-configured path
	if b, err := os.ReadFile(m.cfg.File); err == nil {
		var list []Subscription
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("parse webhooks file: %w", err)
		}
		for i := range list {
			s := list[i]
			m.subs[s.ID] = &s
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// #nosec G304 -- operator-configured path
	if b, err := os.ReadFile(m.deliveriesPath()); err == nil {
		var list []Delivery
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("parse webhook deliveries file: %w", err)
		}
		for i := range list {
			d := list[i]
			m.deliv[d.ID] = &d
			m.order = append(m.order, d.ID)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) saveSubsLocked() error {
	if m.cfg.File == "" {
		return nil
	}
	list := make([]Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		list = append(list, *s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return writeAtomic(m.cfg.File, list)
}

func (m *Manager) flushDeliveries() {
	m.mu.Lock()
	if !m.dirty || m.cfg.File == "" {
		m.mu.Unlock()
		return
	}
	list := make([]Delivery, 0, len(m.order))
	for _, id := range m.order {
		list = append(list, *m.deliv[id])
	}
	m.dirty = false
	m.mu.Unlock()
	if err := writeAtomic(m.deliveriesPath(), list); err != nil {
		m.cfg.Log.Warn("webhook delivery log persist failed", "error", err)
	}
}

// Start launches the delivery workers and retry scheduler; it blocks until
// ctx is canceled, then flushes the delivery log. Run it in a goroutine.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < m.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case did := <-m.queue:
					m.attempt(ctx, did)
				}
			}
		}()
	}
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	m.scan()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			m.flushDeliveries()
			return
		case <-t.C:
			m.scan()
			m.flushDeliveries()
		}
	}
}

// scan enqueues deliveries whose next attempt is due.
func (m *Manager) scan() {
	now := m.cfg.Now()
	var due []string
	m.mu.Lock()
	for _, id := range m.order {
		d := m.deliv[id]
		if (d.Status == StatusPending || d.Status == StatusRetrying) && !m.busy[id] && (d.NextAttemptAt == nil || !d.NextAttemptAt.After(now)) {
			m.busy[id] = true
			due = append(due, id)
		}
	}
	m.mu.Unlock()
	for _, id := range due {
		select {
		case m.queue <- id:
		default:
			m.mu.Lock()
			delete(m.busy, id) // queue full; the next tick retries
			m.mu.Unlock()
		}
	}
}

// Matches reports whether a subscription's patterns select eventType.
func Matches(patterns []string, eventType string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if p == "*" {
			return true
		}
		if ok, _ := path.Match(p, eventType); ok {
			return true
		}
	}
	return false
}

// Dispatch is the events.Bus sink: it queues a delivery for every enabled
// subscription of the event's project whose patterns match. It never blocks.
func (m *Manager) Dispatch(e events.Event) {
	project, _ := e.Data["project"].(string)
	if project == "" {
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	m.mu.Lock()
	for _, s := range m.subs {
		if s.Enabled && s.Project == project && Matches(s.EventTypes, e.Type) {
			m.enqueueLocked(s, e, body)
		}
	}
	m.mu.Unlock()
}

func (m *Manager) enqueueLocked(s *Subscription, e events.Event, body []byte) *Delivery {
	now := m.cfg.Now()
	d := &Delivery{ID: id.New(), SubscriptionID: s.ID, Project: s.Project, EventID: e.ID, EventType: e.Type,
		Status: StatusPending, CreatedAt: now, UpdatedAt: now, NextAttemptAt: &now, Payload: body}
	m.deliv[d.ID] = d
	m.order = append(m.order, d.ID)
	m.dirty = true
	m.evictLocked()
	return d
}

// evictLocked trims the log to MaxDeliveries, dropping the oldest finished
// deliveries (succeeded before dead); in-flight ones are never evicted.
func (m *Manager) evictLocked() {
	for len(m.order) > m.cfg.MaxDeliveries {
		victim := -1
		for i, id := range m.order {
			if m.deliv[id].Status == StatusSucceeded {
				victim = i
				break
			}
		}
		if victim < 0 {
			for i, id := range m.order {
				if m.deliv[id].Status == StatusDead {
					victim = i
					break
				}
			}
		}
		if victim < 0 {
			return
		}
		delete(m.deliv, m.order[victim])
		m.order = append(m.order[:victim], m.order[victim+1:]...)
	}
}

// Sign returns the X-Kryton-Signature value for body at unix timestamp ts:
// "sha256=" + hex(HMAC-SHA256(secret, ts + "." + body)).
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) attempt(ctx context.Context, did string) {
	m.mu.Lock()
	d := m.deliv[did]
	if d == nil || (d.Status != StatusPending && d.Status != StatusRetrying) {
		delete(m.busy, did)
		m.mu.Unlock()
		return
	}
	sub := m.subs[d.SubscriptionID]
	var subCopy Subscription
	if sub != nil {
		subCopy = *sub
	}
	body := append([]byte(nil), d.Payload...)
	attemptNo := d.Attempts + 1
	eventID, eventType, project := d.EventID, d.EventType, d.Project
	m.mu.Unlock()

	var status int
	var errMsg string
	start := time.Now()
	switch {
	case sub == nil:
		errMsg = "subscription deleted"
	case !subCopy.Enabled:
		errMsg = "subscription disabled"
	default:
		status, errMsg = m.post(ctx, subCopy, did, eventID, eventType, attemptNo, body)
	}
	dur := time.Since(start).Milliseconds()

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.busy, did)
	d = m.deliv[did]
	if d == nil {
		return
	}
	now := m.cfg.Now()
	d.Attempts = attemptNo
	d.UpdatedAt = now
	d.LastStatusCode = status
	d.LastError = errMsg
	d.AttemptLog = append(d.AttemptLog, Attempt{Time: now, StatusCode: status, Error: errMsg, DurationMs: dur})
	if len(d.AttemptLog) > maxAttemptLog {
		d.AttemptLog = d.AttemptLog[len(d.AttemptLog)-maxAttemptLog:]
	}
	m.dirty = true
	outcome := "failure"
	switch {
	case errMsg == "":
		d.Status, d.NextAttemptAt = StatusSucceeded, nil
		outcome = "success"
	case sub == nil || !subCopy.Enabled || attemptNo > len(m.cfg.Backoff):
		d.Status, d.NextAttemptAt = StatusDead, nil
		outcome = "dead"
	default:
		next := now.Add(m.cfg.Backoff[attemptNo-1])
		d.Status, d.NextAttemptAt = StatusRetrying, &next
	}
	if m.cfg.OnAttempt != nil {
		m.cfg.OnAttempt(project, outcome)
	}
}

func (m *Manager) post(ctx context.Context, s Subscription, deliveryID, eventID, eventType string, attemptNo int, body []byte) (int, string) {
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	ts := m.cfg.Now().Unix()
	req.Header.Set("Content-Type", "application/cloudevents+json")
	req.Header.Set("User-Agent", "Kryton-Webhooks/1")
	req.Header.Set("X-Kryton-Event-ID", eventID)
	req.Header.Set("X-Kryton-Event-Type", eventType)
	req.Header.Set("X-Kryton-Delivery-ID", deliveryID)
	req.Header.Set("X-Kryton-Attempt", strconv.Itoa(attemptNo))
	req.Header.Set("X-Kryton-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Kryton-Signature", Sign(s.Secret, ts, body))
	res, err := m.client.Do(req)
	if err != nil {
		return 0, truncate(err.Error(), 300)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxResponseBytes))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res.StatusCode, ""
	}
	return res.StatusCode, fmt.Sprintf("endpoint returned HTTP %d", res.StatusCode)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// NewSecret returns a random signing secret.
func NewSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b[:])
}

// Validate checks a subscription's URL (via the guard) and patterns.
func (m *Manager) Validate(url string, patterns []string) error {
	if err := m.cfg.Guard.ValidateURL(url); err != nil {
		return err
	}
	if len(patterns) > 20 {
		return errors.New("at most 20 eventTypes patterns")
	}
	for _, p := range patterns {
		if p == "" {
			return errors.New("eventTypes entries must not be empty")
		}
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("invalid eventTypes pattern %q", p)
		}
	}
	return nil
}

// Create validates and stores a new subscription. A secret is generated when
// s.Secret is empty; the returned copy is the only place it is exposed.
func (m *Manager) Create(s Subscription) (Subscription, error) {
	if err := m.Validate(s.URL, s.EventTypes); err != nil {
		return Subscription{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.subs {
		if x.Project == s.Project {
			n++
		}
	}
	if n >= MaxSubscriptionsPerProject {
		return Subscription{}, ErrLimit
	}
	now := m.cfg.Now()
	s.ID, s.CreatedAt, s.UpdatedAt, s.Enabled = id.New(), now, now, true
	if s.Secret == "" {
		s.Secret = NewSecret()
	}
	if s.EventTypes == nil {
		s.EventTypes = []string{}
	}
	m.subs[s.ID] = &s
	if err := m.saveSubsLocked(); err != nil {
		delete(m.subs, s.ID)
		return Subscription{}, err
	}
	return s, nil
}

// Patch carries optional updates; nil fields are left unchanged.
type Patch struct {
	URL         *string
	Description *string
	EventTypes  *[]string
	Enabled     *bool
}

// Update applies p to a subscription.
func (m *Manager) Update(subID string, p Patch) (Subscription, error) {
	m.mu.Lock()
	cur, ok := m.subs[subID]
	if !ok {
		m.mu.Unlock()
		return Subscription{}, ErrNotFound
	}
	next := *cur
	m.mu.Unlock()
	if p.URL != nil {
		next.URL = *p.URL
	}
	if p.Description != nil {
		next.Description = *p.Description
	}
	if p.EventTypes != nil {
		next.EventTypes = *p.EventTypes
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if err := m.Validate(next.URL, next.EventTypes); err != nil {
		return Subscription{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok = m.subs[subID]
	if !ok {
		return Subscription{}, ErrNotFound
	}
	next.UpdatedAt = m.cfg.Now()
	old := *cur
	*cur = next
	if err := m.saveSubsLocked(); err != nil {
		*cur = old
		return Subscription{}, err
	}
	return next.Public(), nil
}

// Delete removes a subscription; its pending deliveries die on next attempt.
func (m *Manager) Delete(subID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.subs[subID]
	if !ok {
		return ErrNotFound
	}
	delete(m.subs, subID)
	if err := m.saveSubsLocked(); err != nil {
		m.subs[subID] = old
		return err
	}
	return nil
}

// Get returns one subscription without its secret.
func (m *Manager) Get(subID string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[subID]
	if !ok {
		return Subscription{}, ErrNotFound
	}
	return s.Public(), nil
}

// List returns subscriptions (secrets removed), oldest first. project ""
// lists all; visible, when set, filters by project.
func (m *Manager) List(project string, visible func(string) bool) []Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Subscription{}
	for _, s := range m.subs {
		if (project == "" || s.Project == project) && (visible == nil || visible(s.Project)) {
			out = append(out, s.Public())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// DeliveryFilter selects deliveries for ListDeliveries.
type DeliveryFilter struct {
	Project        string
	SubscriptionID string
	Status         string
	Visible        func(string) bool
	Limit          int
	Cursor         string
}

// ListDeliveries returns deliveries newest first with offset-based paging
// over the (bounded) log; the cursor is opaque.
func (m *Manager) ListDeliveries(f DeliveryFilter) ([]Delivery, string, error) {
	limit := f.Limit
	if limit < 1 || limit > 500 {
		limit = 50
	}
	start := 0
	if f.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil {
			return nil, "", errors.New("invalid cursor")
		}
		n, err := strconv.Atoi(string(b))
		if err != nil || n < 0 {
			return nil, "", errors.New("invalid cursor")
		}
		start = n
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []Delivery
	for i := len(m.order) - 1; i >= 0; i-- {
		d := m.deliv[m.order[i]]
		if f.Project != "" && d.Project != f.Project {
			continue
		}
		if f.SubscriptionID != "" && d.SubscriptionID != f.SubscriptionID {
			continue
		}
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		if f.Visible != nil && !f.Visible(d.Project) {
			continue
		}
		matched = append(matched, d.WithoutPayload())
	}
	if start >= len(matched) {
		return []Delivery{}, "", nil
	}
	end := start + limit
	next := ""
	if end < len(matched) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	} else {
		end = len(matched)
	}
	return matched[start:end], next, nil
}

// GetDelivery returns one delivery including payload and attempt log.
func (m *Manager) GetDelivery(did string) (Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deliv[did]
	if !ok {
		return Delivery{}, ErrNotFound
	}
	return *d, nil
}

// Redeliver resets a delivery (typically dead) for a fresh round of retries.
func (m *Manager) Redeliver(did string) (Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deliv[did]
	if !ok {
		return Delivery{}, ErrNotFound
	}
	if d.Status == StatusPending || d.Status == StatusRetrying {
		return *d, nil
	}
	if _, ok := m.subs[d.SubscriptionID]; !ok {
		return Delivery{}, fmt.Errorf("subscription %s no longer exists", d.SubscriptionID)
	}
	now := m.cfg.Now()
	d.Status, d.Attempts, d.LastError, d.LastStatusCode = StatusPending, 0, "", 0
	d.NextAttemptAt, d.UpdatedAt = &now, now
	m.dirty = true
	return *d, nil
}

// SendTest queues a synthetic io.kryton.webhook.test event for one subscription.
func (m *Manager) SendTest(subID string) (Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[subID]
	if !ok {
		return Delivery{}, ErrNotFound
	}
	e := events.Event{SpecVersion: "1.0", ID: id.New(), Source: "kryton://control-plane", Type: "io.kryton.webhook.test",
		Subject: "webhooks/" + s.ID, Time: m.cfg.Now(), DataContentType: "application/json",
		Data: map[string]any{"project": s.Project, "subscriptionId": s.ID, "message": "test delivery"}}
	body, _ := json.Marshal(e)
	return *m.enqueueLocked(s, e, body), nil
}

// DeadLetterCount returns the number of dead deliveries per project.
func (m *Manager) DeadLetterCount() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, d := range m.deliv {
		if d.Status == StatusDead {
			out[d.Project]++
		}
	}
	return out
}
