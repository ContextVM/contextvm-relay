package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	kindServerAnnouncement = 11316
	kindContextVMMessage   = 25910
)

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
	pubkeys, err := getAnnouncedServers(ctx)
	if err != nil {
		log.Printf("[ERROR] heartbeat listing announcements: %v", err)
		return
	}

	for _, serverPubkey := range pubkeys {
		checkCtx, cancel := context.WithTimeout(ctx, h.cfg.RequestTimeout)
		healthy := h.pingServer(checkCtx, serverPubkey)
		cancel()

		if healthy {
			h.resetFailures(serverPubkey)
			continue
		}

		fails := h.incrementFailures(serverPubkey)
		log.Printf("[WARN] heartbeat failed for %s (%d/%d)", serverPubkey, fails, h.cfg.FailThreshold)
		if fails >= h.cfg.FailThreshold {
			deleted, delErr := deleteAllEventsByAuthor(ctx, serverPubkey)
			if delErr != nil {
				log.Printf("[ERROR] deleting events for %s: %v", serverPubkey, delErr)
				continue
			}
			h.resetFailures(serverPubkey)
			log.Printf("[INFO] purged %d event(s) for unresponsive server %s", deleted, serverPubkey)
		}
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

func (h *heartbeatManager) pingServer(ctx context.Context, serverPubkey string) bool {
	relay, err := nostr.RelayConnect(ctx, h.cfg.RelayURL)
	if err != nil {
		log.Printf("[ERROR] heartbeat relay connect failed: %v", err)
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
		log.Printf("[ERROR] heartbeat publish failed: %v", err)
		return false
	}

	filter := nostr.Filter{
		Kinds:   []int{kindContextVMMessage, nostr.KindGiftWrap},
		Authors: []string{serverPubkey},
		Tags:    nostr.TagMap{"e": []string{req.ID}},
	}

	sub, err := relay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		log.Printf("[ERROR] heartbeat subscribe failed: %v", err)
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
