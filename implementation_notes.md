# BitBangProxy Implementation Notes

Technical notes on non-obvious design decisions and mechanisms. Each section explains the problem, why it exists, and how it's solved.

## Session ID Routing (sw.js)

**Problem:** The service worker intercepts all `/__device__/*` requests and routes them to the correct bootstrap page. With multiple tabs open, the SW needs to know which tab owns each request.

**Mechanism:** Each bootstrap page generates a random 8-hex-char session ID and includes it in the iframe URL: `/__device__/<sessionId>/path`. The bootstrap registers the session ID with the SW via `postMessage`. The SW extracts the session ID from each request URL and looks up the corresponding bootstrap client.

**Why not cookies?** We tried using a real browser cookie (`__bb_sid`) to pass the session ID. Service workers don't reliably see cookies in `event.request.headers` for programmatically initiated requests (like setting `iframe.src`). The URL-based approach works consistently.

**Why not client ID tracking?** Browser client IDs change on every navigation. Form submissions, redirects, and page loads all create new client IDs. Tracking them is fragile and leads to race conditions.

## XHR/Fetch URL Shim (xhr-shim.js)

**Problem:** Proxied web apps use absolute paths (`/scripts/foo.js`, `/api/data`, `/login`) in XHR and fetch calls. These escape the `/__device__/` scope and go directly to the signaling server. Synchronous XHR (used by some routers/embedded devices) bypasses the service worker entirely, making SW-level interception impossible.

**Mechanism:** `xhr-shim.js` is injected into HTML pages alongside `ws-shim.js`. It patches `XMLHttpRequest.prototype.open` and `window.fetch` to rewrite absolute-path URLs to `/__device__/<sessionId>/path` before the request fires. The session ID is set via an inline `<script>window.__bbSessionId='...';</script>` tag prepended by the SW.

**Why not SW-level redirect?** The previous approach used the SW to intercept absolute-path requests and return 307 redirects. This had multiple problems: (1) synchronous XHR bypasses the SW entirely per spec, (2) 307 redirects can strip POST bodies in some browsers, (3) race conditions on session ID lookup when XHR fires before the SW can resolve the requesting client. The shim fixes URLs at the source, eliminating all three issues.

**What it patches:** `XMLHttpRequest.prototype.open` (catches both sync and async XHR) and `window.fetch`. Relative URLs, data: URLs, and external URLs are passed through unchanged. URLs already prefixed with `/__device__/` or `/__bitbang__/` are not rewritten.

**What it doesn't handle:** URLs in HTML attributes (`<a href>`, `<form action>`, `<img src>`) loaded from the initial HTML are not patched — these resolve relative to the iframe's `/__device__/<sessionId>/<target>/` base URL, so they work correctly without rewriting. Only programmatic requests (XHR/fetch) need the shim.

## Cookie Jar (sw.js)

**Problem:** Service worker synthetic responses (`new Response(...)`) strip `Set-Cookie` headers. The browser's native cookie handling doesn't work for proxied content. Web apps that use sessions/login are broken.

**Mechanism:** The SW maintains its own cookie jar, keyed by `uid:target` to prevent cross-device leakage while allowing cross-tab sharing for the same device.

On responses: the SW extracts `Set-Cookie` headers, parses them (name, value, path, max-age, expires), and stores them in the jar. The headers are removed before creating the Response.

On requests: the SW looks up matching cookies for the request path and injects a `Cookie` header into the request metadata sent to the bootstrap. The bootstrap includes all headers in the SWSP SYN frame. The device/proxy reads the Cookie header and passes it to the app.

**Multiple Set-Cookie:** HTTP allows multiple `Set-Cookie` headers. Both the Python adapter and Go proxy serialize these as JSON arrays in the SWSP response, and the SW cookie jar processes each one.

**HttpOnly:** Works correctly because the browser never sees the cookies. The SW jar manages them entirely, so HttpOnly flags are irrelevant.

**Lifetime:** Cookies persist for the SW's lifetime (until the browser tab closes or the worker terminates). They don't persist across WebRTC reconnections.

## SWSP Request Header Forwarding

**Problem:** The original SWSP protocol only carried `method`, `pathname`, `contentType`, and `contentLength` in request frames. Request headers (Cookie, Authorization, custom headers) were dropped.

**Mechanism:** The bootstrap now includes all request headers in the SWSP SYN frame's JSON payload as a `headers` field. The Python adapter injects them into the WSGI environ (`HTTP_*` keys per WSGI spec) or ASGI scope (header tuples). The Go proxy forwards them as HTTP request headers.

**Backward compatible:** Devices that don't read the `headers` field continue to work. The `contentType` and `contentLength` fields are preserved for older devices.

## Cross-Host Redirect Handling (Go proxy)

**Problem:** Some servers redirect to a different host (e.g., `nas.local` port 80 redirects to `nas.local:5000`). Following all redirects server-side breaks relative path resolution in the browser. Not following any redirects breaks cross-host targets.

**Mechanism:** The proxy distinguishes two types of redirects:

1. **Cross-host redirects** (different host or port): Followed server-side. The proxy updates `connTarget` and `targetPrefix`. Example: `nas.local` -> `nas.local:5000`.

2. **Same-host redirects** (same host, same scheme): Passed back to the browser with the Location header rewritten to just the path. The xhr-shim rewrites the path to `/__device__/<sessionId>/path` when the browser follows the redirect. This preserves correct iframe URL for relative path resolution. Example: `/webman/logout.cgi` -> `/`.

**Location rewriting:** The proxy strips the scheme and host from redirect Location headers (`http://nas.local:5000/` -> `/`). Without this, the browser would try to navigate directly to the target (mixed content error, CSP violation, bypasses proxy).

## Target Probe on Connect (Go proxy)

**Problem:** When the user enters `nas.local` (no port), the first request to `http://nas.local/` triggers a cross-host redirect to `nas.local:5000`. But concurrent requests (favicon, CSS, JS) may fire before the redirect updates the target, causing them to hit port 80 and fail.

**Mechanism:** During the connect handshake, before sending `ready`, the proxy makes a HEAD request to the target. If the probe triggers a cross-host redirect, the target is updated immediately. All subsequent requests use the correct target from the start.

## Connect Prefix vs Target Prefix (Go proxy)

**Problem:** After a cross-host redirect (e.g., `nas.local` -> `nas.local:5000`), the `targetPrefix` updates to `/nas.local:5000`. But the iframe URL still contains the original target (`/__device__/<sid>/nas.local/...`). Subsequent requests arrive with `/nas.local/...` which doesn't match the new prefix.

**Mechanism:** The proxy tracks both `targetPrefix` (current, updated on redirect) and `connectPrefix` (original, never changes). `resolveTarget` checks both: first the current prefix, then the original. Either way, it uses the current `connTarget` for the actual HTTP request.

## Dynamic Target Routing (Go proxy)

**Problem:** The proxy needs to extract the target server from the URL path. `https://bitba.ng/<uid>/nas.local:8080/admin` should proxy `/admin` from `nas.local:8080`.

**Mechanism:** `parseTargetFromPath` extracts the first path segment if it looks like a hostname (contains a colon for `host:port`, a dot for `nas.local`/`192.168.1.10`, or is `localhost`).

When `--target` is set, all requests go to that fixed target. When not set, the target is extracted from the connect path.

**Landing page:** When no target is in the URL, the proxy serves a built-in HTML page with a text input. The user enters a target, and the page redirects the top-level window to `/<uid>/<target>`.

## WSGI Threading (Python adapter)

**Problem:** WSGI apps (Flask) can block with `time.sleep()` in streaming responses (SSE). Running the WSGI app synchronously in the asyncio event loop stalls all other operations.

**Mechanism:** The WSGI app runs in a background thread. Frames are passed to the event loop via a bounded `queue.Queue(maxsize=64)`. The event loop reads frames with `loop.run_in_executor(None, frame_queue.get)` which wraps the blocking get in a thread pool. The bounded queue provides backpressure -- the WSGI thread blocks on `put()` when the data channel can't keep up.

## ASGI Streaming (Python adapter)

**Problem:** The original ASGI adapter collected the entire response body before sending any SWSP frames. Streaming responses (SSE) would hang.

**Mechanism:** The ASGI app runs as a concurrent `asyncio.create_task`. The `send()` callback pushes frames directly onto a bounded `asyncio.Queue(maxsize=64)`. The outer async generator yields frames as they arrive. Backpressure works naturally -- `await frame_queue.put()` blocks the `send()` callback when the queue is full, pausing the ASGI app.

## WebSocket Bridging

**Problem:** WebSocket connections from proxied apps need to work through the SWSP data channel.

**Browser side:** A `ws-shim.js` script is injected into HTML pages by the SW. It replaces `window.WebSocket` with a shim that communicates with the bootstrap via `postMessage`. Each WebSocket allocates a SWSP stream ID. Messages carry a type byte prefix (0=text, 1=binary) in DAT frames.

**Injection:** The SW prepends `xhr-shim.js` and `ws-shim.js` script tags to HTML responses, but only for navigation requests (`event.request.mode === 'navigate'`). This prevents injection into XHR/fetch responses that happen to have `text/html` content type (e.g., NAS API endpoints). The xhr-shim is loaded first with a preceding inline script that sets `window.__bbSessionId`.

**Go proxy:** Detects WebSocket SYN frames (`"type": "websocket"` in payload), opens a real WebSocket to the local server, and bridges messages bidirectionally. FIN from either side closes both ends.

## Favicon Handling (bootstrap.js)

**Problem:** Different apps set favicons differently. Some serve `/favicon.ico` (fileshare, webcam). Others set `<link rel="icon">` dynamically via JavaScript (NAS). The browser tab favicon comes from the top-level page (bootstrap), not the iframe.

**Mechanism:** The bootstrap sets `/__device__/<sid>/favicon.ico` immediately on iframe load (works for apps with a static favicon). It also uses a `MutationObserver` on the iframe's `<head>` to detect dynamically added `<link rel="icon">` elements. If found, the bootstrap copies the href to its own favicon link, overriding the `/favicon.ico` fallback. The observer disconnects after 5 seconds to avoid permanent overhead.

## HTTPS Detection (Go proxy)

**Problem:** Some public servers require HTTPS. The proxy uses HTTP by default (appropriate for local targets). Attempting HTTP against an HTTPS-only server causes redirect loops.

**Mechanism:** The probe on connect detects HTTPS redirects. If the target redirects HTTP to HTTPS, the proxy sends an error to the browser via the control channel: "requires HTTPS, which is not currently supported." The browser displays this using the bootstrap's generic error screen.

**Why not support HTTPS?** HTTPS support was implemented and tested but caused a regression with the WebSocket shim in nested proxy scenarios. Since the proxy is designed for local network targets (which use HTTP), HTTPS support was deferred. The error message gives a clear explanation rather than a broken page.

## Connect Handshake (bootstrap.js + device)

**Problem:** The device needs to know the requested path before serving content. This enables path-based routing and future PIN protection.

**Mechanism:** When the data channel opens, the browser sends a control message on stream ID 0: `{ "type": "connect", "path": "/admin" }`. The device responds with `{ "type": "ready" }` (or `{ "type": "error", "message": "..." }`). The browser waits for the response before creating the iframe.

**Stream ID 0:** Reserved for control messages. Both the Python adapter and Go proxy handle stream 0 separately from HTTP traffic.

## Multiple Set-Cookie Response Headers

**Problem:** HTTP allows multiple `Set-Cookie` headers in a response (one per cookie). SWSP response headers are serialized as JSON objects, which collapse duplicate keys.

**Mechanism:** Both the Python adapter and Go proxy detect multiple `Set-Cookie` values and serialize them as a JSON array instead of a single string. The SW cookie jar's `storeCookies` function handles both formats (`Array.isArray` check).

## Service Worker Error Handling

**Problem:** When the browser aborts a request (e.g., during page navigation), SWSP response frames may still arrive. Calling `enqueue()` or `close()` on an already-closed ReadableStream throws.

**Mechanism:** The SW wraps `streamController.enqueue(data)` and `streamController.close()` in try/catch blocks to silently ignore these race conditions.

## PIN Authentication

**Problem:** Devices and proxies need optional access control without involving the signaling server.

**Mechanism:** PIN auth uses the DTLS-encrypted data channel (stream 0 control messages). The signaling server never sees the PIN.

Flow: device sends `auth_required` -> browser shows PIN prompt -> user enters PIN -> browser sends `auth` message -> device verifies -> sends `auth_result`.

**Python adapter** supports both a simple `pin='1234'` argument and a `pin_callback=func(path, pin) -> bool` for per-path logic. The callback is called with an empty PIN during connect to determine if auth is needed for a given path (`_pin_required`). If the callback returns True for empty PIN, that path is unprotected.

**Go proxy** supports `--pin` flag via the `auth.PINAuth` type.

**Session caching:** After successful auth, bootstrap.js stores the PIN in `sessionStorage` (keyed by UID). On page refresh, the cached PIN is auto-submitted. If the cached PIN fails (e.g., navigating to a path with a different PIN), the prompt appears cleanly without error messages.

**Timing defense:** Both success and failure show a spinner with a delay (2s success, 3s failure) to prevent timing-based PIN guessing.

## In-App Navigation with PIN (bb-navigate)

**Problem:** Clicking a link to a PIN-protected path within the app should prompt for PIN without dropping the WebRTC connection.

**Mechanism:** The iframe sends `window.parent.postMessage({ type: 'bb-navigate', path: '/admin' }, '*')` to the bootstrap. The bootstrap sends a new `connect` message on stream 0 with the new path. The device re-evaluates the PIN callback. If auth is needed, the PIN prompt appears. After success, the iframe navigates to the new path. The WebRTC connection stays up throughout.

This is also used by the Go proxy's landing page (URL input) to navigate to the target without reconnecting.

## Same-Host vs Cross-Host Redirects (Go proxy)

**Problem:** Local servers may redirect (e.g., NAS logout redirects `/webman/logout.cgi` to `/`). Following all redirects server-side breaks the iframe's URL context, causing relative path resolution errors. Not following any redirects breaks cross-host targets (e.g., `nas.local` -> `nas.local:5000`).

**Mechanism:** The proxy's `http.Client.CheckRedirect` distinguishes:
- **Cross-host redirects** (different host/port): followed server-side, target updated
- **Same-host redirects** (same host, same scheme): passed back to browser as a 302 with Location rewritten to just the path. The xhr-shim rewrites the path when the browser follows the redirect. This preserves the correct iframe URL for relative path resolution.

**Location rewriting:** Full URLs like `http://nas.local:5000/` are stripped to just the path `/`. Without this, the browser would navigate directly to the target, bypassing the proxy.

## Favicon Handling

**Problem:** Different apps handle favicons differently. Some serve `/favicon.ico` (fileshare, webcam). Some set `<link rel="icon">` dynamically via JavaScript (NAS). The browser tab favicon comes from the top-level page, not the iframe.

**Mechanism:** On `iframe.onload`, bootstrap.js fetches `/__device__/<sid>/favicon.ico`. Only if the response is 200 does it set the favicon link (avoids clearing the default icon with a 404). It also uses a `MutationObserver` on the iframe's `<head>` to detect dynamically added `<link rel="icon">` elements (e.g., NAS). If found, the favicon updates. The observer disconnects after 5 seconds.

The signaling server's favicon persists as the default (from the initial page load) until a device favicon overrides it.

## X-Frame-Options Stripping (Go proxy)

**Problem:** Some servers set `X-Frame-Options: deny` which prevents their content from loading in the proxy's iframe.

**Mechanism:** The Go proxy strips `X-Frame-Options` headers from responses. This is standard reverse proxy behavior -- the content is intentionally being displayed in an iframe. `Content-Security-Policy` headers are NOT stripped as this caused regressions with nested proxy scenarios.

## Request Header Rewriting (Go proxy)

**Problem:** Browser headers forwarded through the SWSP data channel contain `Host: bitba.ng`, `Origin: https://bitba.ng`, and `Referer: https://bitba.ng/...`. Many local servers use these headers for routing and access control. TP-Link routers, for example, return "Not Found" for requests with a foreign Host header and 404 for sub-resources without a same-origin Referer.

**Mechanism:** The Go proxy skips `Host`, `Origin`, `Referer`, and `Content-Length` when forwarding browser headers, and sets them to match the target:
- `Host` is set to the target via Go's `Request.Host` field
- `Referer` is set to `http://<target>/`
- `Content-Length` is computed from the buffered body (see below)

All other browser headers (Cookie, Content-Type, Accept, etc.) are forwarded as-is.

## POST Body Buffering (Go proxy)

**Problem:** When the POST body arrives via an `io.Pipe` (streamed from the data channel), Go's HTTP client doesn't know the content length upfront and uses `Transfer-Encoding: chunked`. Many embedded web servers (TP-Link routers, NAS devices) don't support chunked encoding and crash or return errors.

**Mechanism:** The proxy reads the entire request body into a `bytes.Buffer` before creating the HTTP request. This gives Go's HTTP client a seekable reader with a known length, so it sends `Content-Length: N` instead of chunked encoding. For typical API calls (small JSON/form bodies), the memory overhead is negligible. Large uploads (file transfers) also buffer, but SWSP's 16KB frame size already limits throughput more than buffering does.

## Sync XHR Handling (xhr-shim.js)

**Problem:** Some web apps (notably TP-Link Omada routers) use synchronous XMLHttpRequest (`async: false`). Sync XHR bypasses the service worker entirely per browser spec, so requests can never reach the WebRTC data channel regardless of URL rewriting.

**Mechanism:** The xhr-shim patches `XMLHttpRequest.prototype.open` to force the `async` parameter to `true` when it's explicitly set to `false`. This allows the request to go through the service worker and data channel. Most apps that use sync XHR (including jQuery's `$.ajax({async: false})`) use callback-based response handling and work correctly when forced async. The sync-to-async conversion is invisible to the calling code in practice.
