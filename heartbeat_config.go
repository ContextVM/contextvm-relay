package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

type heartbeatConfig struct {
	Enabled        bool
	Interval       time.Duration
	RequestTimeout time.Duration
	FailThreshold  int
	ClientSK       string
	ClientPubkey   string
	RelayURL       string
}

func loadHeartbeatConfig(port string) heartbeatConfig {
	enabled := envBool("CVM_HEARTBEAT_ENABLED", true)
	interval := envDuration("CVM_HEARTBEAT_INTERVAL", 5*time.Hour)
	timeout := envDuration("CVM_HEARTBEAT_REQUEST_TIMEOUT", 12*time.Second)
	threshold := envInt("CVM_HEARTBEAT_FAIL_THRESHOLD", 2)
	relayURL := envString("CVM_HEARTBEAT_RELAY_URL", fmt.Sprintf("ws://localhost:%s", port))
	clientSK := os.Getenv("CVM_HEARTBEAT_CLIENT_SK")
	if enabled && clientSK == "" {
		clientSK = nostr.GeneratePrivateKey()
		log.Printf("[INFO] CVM_HEARTBEAT_CLIENT_SK not set; generated ephemeral heartbeat key")
	}

	clientPK, err := nostr.GetPublicKey(clientSK)
	if enabled && err != nil {
		log.Printf("[WARN] CVM_HEARTBEAT_CLIENT_SK invalid; generated new heartbeat key")
		clientSK = nostr.GeneratePrivateKey()
		clientPK, err = nostr.GetPublicKey(clientSK)
		if err != nil {
			log.Printf("[WARN] failed to derive heartbeat public key from generated key; disabling heartbeat")
			enabled = false
		}
	}

	return heartbeatConfig{
		Enabled:        enabled,
		Interval:       interval,
		RequestTimeout: timeout,
		FailThreshold:  threshold,
		ClientSK:       clientSK,
		ClientPubkey:   clientPK,
		RelayURL:       relayURL,
	}
}

func envString(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("[WARN] invalid duration for %s=%q; using %s", key, v, fallback)
		return fallback
	}
	return d
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		log.Printf("[WARN] invalid integer for %s=%q; using %d", key, v, fallback)
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("[WARN] invalid boolean for %s=%q; using %t", key, v, fallback)
		return fallback
	}
	return b
}
