# l0-memory :: Web Clipper (browser extension)

Save a text selection or a note from any page straight into your local
l0-memory store. Manifest V3; works in Chrome / Edge / Brave (and Firefox with
minor packaging). Fully offline — it talks only to `ltm serve` on
`127.0.0.1`, never to the network.

```
page selection ──▶ extension ──▶ http://127.0.0.1:8080 (ltm serve) ──▶ SQLite store
```

## Prerequisites

- The `ltm` binary built/installed (`make install` from the repo root puts it at
  `~/.local/bin/ltm`).

## Setup (5 steps)

1. **Start the local server** — it must be running whenever you clip:

   ```sh
   ltm serve            # listens on http://127.0.0.1:8080 (pass a port to change it)
   ```

   On start it prints an **auth token**:

   ```
   REST server listening on http://127.0.0.1:8080
   Auth token: 9f3c…                       ← copy this
     (source: ~/.long-term-memory/serve-token — paste it into the Web Clipper extension)
   ```

   The token is generated once and persisted at `~/.long-term-memory/serve-token`
   (next to `memories.db`), so it stays the same across restarts. To pin your own,
   set `LTM_SERVE_TOKEN` in the environment instead.

2. **Load the extension** — open `chrome://extensions`, enable **Developer mode**
   (top-right), click **Load unpacked**, and select this `extension-browser/`
   folder.

3. **Paste the token** — click the 🧠 toolbar icon and paste the token into the
   **Server token** field. It's saved locally (`chrome.storage.local`) so you only
   do this once (and again if the token changes).

4. **Clip.** Two ways:
   - **Popup**: click the icon, fill Key / Content / Scope / Tags, **Save to Memory**.
   - **Right-click** a selection → **Save selection to l0-memory** (quick save; the
     toolbar badge flashes ✓ on success, ! on failure).

5. Clips land in scope **`web`** by default (change it in the popup). They do
   **not** go into your `user` persona scope, so they never pollute the context the
   recall hook injects into every session.

## Security model

- The server binds to `127.0.0.1` only — not reachable from the network.
- **127.0.0.1 alone is not enough**: any web page you visit could otherwise call
  `http://127.0.0.1:8080` from its own JavaScript. So every request (except
  `GET /health`) must carry the **token** via `X-LTM-Token`. A page that doesn't
  know the token gets `401`.
- CORS is restricted: the server only returns `Access-Control-Allow-Origin` for
  `chrome-extension://` / `moz-extension://` origins. Arbitrary web origins are
  refused by the browser even before the token check — defense in depth.
- The token is stored `0600` on disk and in the extension's local storage; it is
  never sent anywhere but `127.0.0.1`.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| "Cannot reach l0-memory at …" | server not running | start `ltm serve` |
| "Invalid server token" / `401` | token missing or stale | re-copy the token from `ltm serve` into the popup |
| Nothing happens on right-click save | no token set, or server down | open the popup, set the token; check `ltm serve` |
| Wrong port | you ran `ltm serve <port>` | the extension is fixed to `:8080` — either run `ltm serve` on 8080, or edit `ENDPOINT` in `popup.js`/`background.js` **and** `host_permissions` in `manifest.json` |

Run `ltm doctor` for a one-shot check of the whole setup (binary, server
reachability, token, MCP registration).
