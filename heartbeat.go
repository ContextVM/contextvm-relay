package main

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	kindServerAnnouncement = 11316
	kindRelayList          = 10002
	kindContextVMMessage   = 25910
)

var defaultBootstrapRelayURLs = []string{
	"wss://relay.damus.io",
	"wss://relay.primal.net",
	"wss://nos.lol",
	"wss://relay.snort.social/",
	"wss://nostr.mom/",
	"wss://nostr.oxtr.dev/",
}

type relayListSource string

const (
	relayListSourceLocal     relayListSource = "local-10002"
	relayListSourceBootstrap relayListSource = "bootstrap-10002"
	relayListSourceFallback  relayListSource = "fallback-config"
)

type serverProbeTarget struct {
	Pubkey string
	Relays []string
	Source relayListSource
}

type heartbeatManager struct {
	cfg       heartbeatConfig
	failMu    sync.Mutex
	failCount map[string]int
}

func newHeartbeatManager(cfg heartbeatConfig) *heartbeatManager {
	return &heartbeatManager{
		cfg:       cfg,
		failCount: make(map[string]int),
	}
}

func (h *heartbeatManager) run(ctx context.Context) {
	ticker := time.NewTicker(h.cfg.Interval)
	defer ticker.Stop()

	// Run one pass shortly after startup.
	h.cycle(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.cycle(ctx)
		}
	}
}

func (h *heartbeatManager) cycle(ctx context.Context) {
	targets, err := h.getProbeTargets(ctx)
	if err != nil {
		log.Printf("[ERROR] heartbeat listing announcements: %v", err)
		return
	}

	for _, target := range targets {
		checkCtx, cancel := context.WithTimeout(ctx, h.cfg.RequestTimeout)
		healthy := h.pingServer(checkCtx, target)
		cancel()

		if healthy {
			h.resetFailures(target.Pubkey)
			continue
		}

		fails := h.incrementFailures(target.Pubkey)
		log.Printf("[WARN] heartbeat failed for %s via %d relay(s) from %s (%d/%d)", target.Pubkey, len(target.Relays), target.Source, fails, h.cfg.FailThreshold)
		if fails >= h.cfg.FailThreshold {
			deleted, delErr := deleteAllEventsByAuthor(ctx, target.Pubkey)
			if delErr != nil {
				log.Printf("[ERROR] deleting events for %s: %v", target.Pubkey, delErr)
				continue
			}
			h.resetFailures(target.Pubkey)
			log.Printf("[INFO] purged %d event(s) for unresponsive server %s", deleted, target.Pubkey)
		}
	}
}

func (h *heartbeatManager) getProbeTargets(ctx context.Context) ([]serverProbeTarget, error) {
	pubkeys, err := getAnnouncedServers(ctx)
	if err != nil {
		return nil, err
	}

	targets := make([]serverProbeTarget, 0, len(pubkeys))
	for _, pubkey := range pubkeys {
		target, err := h.resolveProbeTarget(ctx, pubkey)
		if err != nil {
			log.Printf("[WARN] heartbeat relay resolution failed for %s: %v", pubkey, err)
			target = h.fallbackProbeTarget(pubkey)
		}

		log.Printf("[INFO] heartbeat probing %s using %s via relays=%v", target.Pubkey, target.Source, target.Relays)
		targets = append(targets, target)
	}

	return targets, nil
}

func (h *heartbeatManager) resolveProbeTarget(ctx context.Context, pubkey string) (serverProbeTarget, error) {
	if relays := h.lookupRelayListLocally(ctx, pubkey); len(relays) > 0 {
		return serverProbeTarget{Pubkey: pubkey, Relays: relays, Source: relayListSourceLocal}, nil
	}

	if relays, err := h.lookupRelayListFromBootstrap(ctx, pubkey); err == nil && len(relays) > 0 {
		return serverProbeTarget{Pubkey: pubkey, Relays: relays, Source: relayListSourceBootstrap}, nil
	} else if err != nil {
		log.Printf("[WARN] bootstrap 10002 lookup failed for %s: %v", pubkey, err)
	}

	return h.fallbackProbeTarget(pubkey), nil
}

func (h *heartbeatManager) fallbackProbeTarget(pubkey string) serverProbeTarget {
	return serverProbeTarget{
		Pubkey: pubkey,
		Relays: []string{normalizeRelayURL(h.cfg.RelayURL)},
		Source: relayListSourceFallback,
	}
}

func getAnnouncedServers(ctx context.Context) ([]string, error) {
	ch, err := db.QueryEvents(ctx, nostr.Filter{Kinds: []int{kindServerAnnouncement}})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for evt := range ch {
		if evt == nil || evt.PubKey == "" {
			continue
		}
		seen[evt.PubKey] = struct{}{}
	}

	pubkeys := make([]string, 0, len(seen))
	for pk := range seen {
		pubkeys = append(pubkeys, pk)
	}
	sort.Strings(pubkeys)
	return pubkeys, nil
}

func deleteAllEventsByAuthor(ctx context.Context, pubkey string) (int, error) {
	ch, err := db.QueryEvents(ctx, nostr.Filter{Authors: []string{pubkey}})
	if err != nil {
		return 0, err
	}

	count := 0
	for evt := range ch {
		if evt == nil {
			continue
		}
		if err := db.DeleteEvent(ctx, evt); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func (h *heartbeatManager) lookupRelayListLocally(ctx context.Context, pubkey string) []string {
	ch, err := db.QueryEvents(ctx, nostr.Filter{Kinds: []int{kindRelayList}, Authors: []string{pubkey}, Limit: 1})
	if err != nil {
		log.Printf("[WARN] local 10002 lookup failed for %s: %v", pubkey, err)
		return nil
	}

	for evt := range ch {
		if evt == nil {
			continue
		}
		if relays := extractOperationalRelays(evt); len(relays) > 0 {
			return relays
		}
	}

	return nil
}

func (h *heartbeatManager) lookupRelayListFromBootstrap(ctx context.Context, pubkey string) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, h.bootstrapLookupTimeout())
	defer cancel()

	pool := nostr.NewSimplePool(queryCtx, nostr.WithPenaltyBox())
	defer pool.Close("heartbeat bootstrap lookup complete")

	filter := nostr.Filter{Kinds: []int{kindRelayList}, Authors: []string{pubkey}, Limit: 1}
	events := pool.FetchMany(queryCtx, defaultBootstrapRelayURLs, filter)
	var latest *nostr.Event
	for evt := range events {
		if evt.Event == nil {
			continue
		}
		if latest == nil || evt.Event.CreatedAt > latest.CreatedAt {
			latest = evt.Event
		}
	}

	if latest != nil {
		if relays := extractOperationalRelays(latest); len(relays) > 0 {
			return relays, nil
		}
	}

	return nil, nil
}

func (h *heartbeatManager) bootstrapLookupTimeout() time.Duration {
	return 60 * time.Second
}

func (h *heartbeatManager) pingServer(ctx context.Context, target serverProbeTarget) bool {
	for _, relayURL := range target.Relays {
		if h.pingServerOnRelay(ctx, relayURL, target.Pubkey) {
			return true
		}
	}
	return false
}

func (h *heartbeatManager) pingServerOnRelay(ctx context.Context, relayURL string, serverPubkey string) bool {
	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		log.Printf("[WARN] heartbeat relay connect failed for %s via %s: %v", serverPubkey, relayURL, err)
		return false
	}
	defer relay.Close()

	reqPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "contextvm-relay-heartbeat",
				"version": version,
			},
		},
	})

	req := nostr.Event{
		Kind:      kindContextVMMessage,
		CreatedAt: nostr.Now(),
		PubKey:    h.cfg.ClientPubkey,
		Tags: nostr.Tags{
			nostr.Tag{"p", serverPubkey},
		},
		Content: string(reqPayload),
	}

	if err := req.Sign(h.cfg.ClientSK); err != nil {
		log.Printf("[ERROR] heartbeat sign failed: %v", err)
		return false
	}

	if err := relay.Publish(ctx, req); err != nil {
		log.Printf("[WARN] heartbeat publish failed for %s via %s: %v", serverPubkey, relayURL, err)
		return false
	}

	filter := nostr.Filter{
		Kinds:   []int{kindContextVMMessage, nostr.KindGiftWrap},
		Authors: []string{serverPubkey},
		Tags:    nostr.TagMap{"e": []string{req.ID}},
	}

	sub, err := relay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		log.Printf("[WARN] heartbeat subscribe failed for %s via %s: %v", serverPubkey, relayURL, err)
		return false
	}
	defer sub.Unsub()

	select {
	case <-ctx.Done():
		return false
	case evt, ok := <-sub.Events:
		return ok && evt != nil
	}
}

func extractOperationalRelays(evt *nostr.Event) []string {
	if evt == nil {
		return nil
	}

	relays := make([]string, 0, len(evt.Tags))
	seen := make(map[string]struct{}, len(evt.Tags))
	for _, tag := range evt.Tags {
		if len(tag) < 2 || tag[0] != "r" {
			continue
		}

		relayURL := normalizeRelayURL(tag[1])
		if relayURL == "" {
			continue
		}

		marker := ""
		if len(tag) > 2 {
			marker = strings.ToLower(strings.TrimSpace(tag[2]))
		}
		if marker == "write" {
			continue
		}

		if _, ok := seen[relayURL]; ok {
			continue
		}
		seen[relayURL] = struct{}{}
		relays = append(relays, relayURL)
	}

	return relays
}

func normalizeRelayURL(url string) string {
	return strings.TrimRight(strings.TrimSpace(url), "/")
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}

func (h *heartbeatManager) incrementFailures(pubkey string) int {
	h.failMu.Lock()
	defer h.failMu.Unlock()
	h.failCount[pubkey]++
	return h.failCount[pubkey]
}

func (h *heartbeatManager) resetFailures(pubkey string) {
	h.failMu.Lock()
	defer h.failMu.Unlock()
	delete(h.failCount, pubkey)
}
