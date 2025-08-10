# Nostr Relay

A simple Nostr relay implementation in Go.

## Features

- Nostr protocol relay implementation
- Event storage using LMDB
- Support for ephemeral events and gift wraps
- Configurable port via command-line flag

## Usage

### Running the Relay

```bash
go run main.go
```

### Specifying a Port

By default, the relay runs on port 3334. You can specify a different port using the `-port` flag:

```bash
go run main.go -port 8080
```

### Building

```bash
go build -o nostr-relay
```

Then run the built binary:

```bash
./nostr-relay -port 8080
```

## Configuration

- **Port**: Configure the listening port with the `-port` flag (default: 3334)
- **Database**: Events are stored in the `./db/` directory (created automatically)

## Dependencies

- `github.com/fiatjaf/eventstore/lmdb` - LMDB event storage
- `github.com/gzuuus/onRelay/atomic` - Atomic circular buffer for ephemeral events
- `github.com/nbd-wtf/go-nostr` - Nostr protocol implementation
- `github.com/pippellia-btc/rely` - Nostr relay framework