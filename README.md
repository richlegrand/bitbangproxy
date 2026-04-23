# BitBangProxy

![Tests](https://github.com/richlegrand/bitbangproxy/actions/workflows/tests.yml/badge.svg)
![License](https://img.shields.io/github/license/richlegrand/bitbangproxy)

A WebRTC proxy that connects browsers to any web server on your local network. No port forwarding, no VPN, no software on the target machine.

BitBangProxy extends [BitBang](https://github.com/richlegrand/bitbang) with a standalone Go binary that proxies HTTP, WebSocket, and streaming connections through a WebRTC data channel.

## Quick start

```bash
# Download the binary for your platform from Releases, then:
./bitbangproxy
```

This prints a URL and waits for connections. Open the URL in a browser, enter a local server address (e.g. `nas.local`, `192.168.1.10:8080`), and you're connected.

Or specify the target directly in the URL:

```
https://bitba.ng/<proxy-id>/nas.local
https://bitba.ng/<proxy-id>/192.168.1.10:8080
https://bitba.ng/<proxy-id>/localhost:3000/admin
```

## Features

- **HTTP proxy** -- GET, POST, uploads, downloads, streaming (SSE)
- **WebSocket proxy** -- bidirectional, multiplexed over the same data channel
- **Dynamic targets** -- target server specified in the URL, no restart needed
- **Cookie/session support** -- login flows work (managed by the service worker)
- **PIN protection** -- optional `--pin` flag to restrict access
- **Redirect handling** -- follows cross-host redirects, passes same-host redirects to the browser
- **No installation on the target** -- proxy runs on any machine on the same network

## Usage

```bash
# Dynamic target (from URL)
./bitbangproxy

# Fixed target
./bitbangproxy --target localhost:8080

# With PIN protection
./bitbangproxy --pin 1234

# Ephemeral identity (new URL each run)
./bitbangproxy --ephemeral

# Custom signaling server
./bitbangproxy --server my-signaling-server.com

# Verbose logging
./bitbangproxy -v
```

## CLI flags

```
--target HOST:PORT   Local server to proxy (default: dynamic from URL)
--pin PIN            PIN to protect proxy access
--ephemeral          Use a temporary identity (new URL each run)
--server HOST        Signaling server hostname (default: bitba.ng)
-v                   Verbose logging and browser debug UI (?debug)
```

When `-v` is enabled, the printed URL includes `?debug`, which activates a browser-side debug UI showing connection steps. Without it, the browser shows a simple "Loading..." while connecting. Verbose mode also logs all HTTP requests and dependency versions at startup.

## How it works

![BitBangProxy Block Diagram](https://raw.githubusercontent.com/richlegrand/bitbangproxy/refs/heads/main/assets/bitbangproxy.png)

The signaling server (`bitba.ng`) brokers the WebRTC handshake, then steps aside. All traffic flows directly between the browser and the proxy via an encrypted data channel (DTLS).

## Building from source

Requires Go 1.19+:

```bash
go build ./cmd/bitbangproxy/
```

Cross-compile for other platforms:

```bash
GOOS=windows GOARCH=amd64 go build -o bitbangproxy.exe ./cmd/bitbangproxy/
GOOS=darwin GOARCH=amd64 go build -o bitbangproxy-macos ./cmd/bitbangproxy/
GOOS=linux GOARCH=amd64 go build -o bitbangproxy ./cmd/bitbangproxy/
```

## Architecture

```
cmd/bitbangproxy/main.go       -- entry point, CLI flags, connection management
internal/identity/identity.go  -- RSA keypair, UID derivation, persistence
internal/signaling/client.go   -- WebSocket signaling, challenge-response auth
internal/peer/connection.go    -- WebRTC peer connection, ICE, SDP
internal/protocol/swsp.go      -- SWSP frame parsing/building
internal/proxy/http.go         -- HTTP proxying, redirects, cookies, landing page
internal/proxy/websocket.go    -- WebSocket bridging
internal/auth/pin.go           -- PIN verification
```

See [implementation_notes.md](implementation_notes.md) for detailed design decisions.

## Related

- [BitBang](https://github.com/richlegrand/bitbang) -- Python library for building BitBang devices
- [BitBang Server](https://github.com/richlegrand/bitbang-server) -- Signaling server and browser runtime

## License

MIT
