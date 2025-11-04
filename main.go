package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/fiatjaf/eventstore/lmdb"
	"github.com/gzuuus/onRelay/atomic"
	"github.com/nbd-wtf/go-nostr"
	"github.com/pippellia-btc/rely"
)

var (
	db     lmdb.LMDBBackend
	buffer *atomic.AtomicCircularBuffer
)

func main() {
	var port string
	flag.StringVar(&port, "port", "3334", "Port to run the relay on")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rely.HandleSignals(cancel)
	db = lmdb.LMDBBackend{Path: "./db/"}
	os.MkdirAll(db.Path, 0o755)
	if err := db.Init(); err != nil {
		panic(err)
	}

	buffer = atomic.NewAtomicCircularBuffer(500)
	relay := rely.NewRelay()
	relay.On.Event = Save
	relay.On.Req = Query
	addr := fmt.Sprintf("localhost:%s", port)
	log.Printf("[INFO] Running relay on %s", addr)

	if err := relay.StartAndServe(ctx, addr); err != nil {
		panic(err)
	}
}

func Save(c rely.Client, e *nostr.Event) error {
	log.Printf("[INFO] Received event (ID: %s, Kind: %d)", e.ID, e.Kind)
	ctx := context.Background()
	switch {
	case nostr.IsEphemeralKind(e.Kind):
		err := buffer.SaveEvent(ctx, e)
		if err != nil {
			log.Printf("[ERROR] storing ephemeral event: %v", err)
			return err
		}
		log.Printf("[INFO] Ephemeral event stored: %s", e.ID)
		return nil

	case e.Kind == nostr.KindGiftWrap:
		err := buffer.SaveEvent(ctx, e)
		if err != nil {
			log.Printf("[ERROR] storing gift wrap event: %v", err)
			return err
		}
		log.Printf("[INFO] Gift wrap event stored: %s", e.ID)
		return nil

	case nostr.IsReplaceableKind(e.Kind):
		return saveReplaceableEvent(ctx, e)

	default:
		return fmt.Errorf("unhandled event kind: %d", e.Kind)
	}
}

func saveReplaceableEvent(ctx context.Context, e *nostr.Event) error {
	err := db.ReplaceEvent(ctx, e)
	if err != nil {
		log.Printf("[ERROR] saving replaceable/addressable event: %v", err)
		return err
	}
	log.Printf("[INFO] Replaceable event saved: %s", e.ID)
	return nil
}

func Query(ctx context.Context, c rely.Client, filters nostr.Filters) ([]nostr.Event, error) {
	log.Printf("[INFO] Received query with filters: %v", filters)
	result := make([]nostr.Event, 0)

	for _, filter := range filters {
		ephemeralEvents, err := buffer.QueryEvents(ctx, filter)
		if err != nil {
			log.Printf("[ERROR] querying ephemeral events: %v", err)
		} else {
			for _, event := range ephemeralEvents {
				if event != nil {
					result = append(result, *event)
				}
			}
		}

		lmdbEvents, err := db.QueryEvents(ctx, filter)
		if err != nil {
			log.Printf("[ERROR] querying LMDB events: %v", err)
		} else {
			for {
				select {
				case event, ok := <-lmdbEvents:
					if !ok {
						lmdbEvents = nil
						break
					}
					result = append(result, *event)
				case <-ctx.Done():
					log.Printf("[INFO] context cancelled while querying LMDB")
					return result, nil
				}
				if lmdbEvents == nil {
					break
				}
			}
		}
	}
	return result, nil
}
