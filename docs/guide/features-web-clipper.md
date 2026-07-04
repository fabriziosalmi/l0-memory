# Local REST API & Web Clipper

**l0-memory** provides a local HTTP REST API daemon and a companion browser extension (Web Clipper) to capture highlights and notes from your browser directly into your local database—entirely offline and airgapped.

---

## 1. Local REST API Daemon (`ltm serve`)

You can run the `ltm` binary as a persistent background daemon that exposes a lightweight HTTP/JSON REST API. This makes it easy for other local programs, scripts, or extensions to write to and read from the memory store without spawning shell subprocesses.

To start the REST server, use the `serve` subcommand:

```sh
ltm serve [port]
```

* By default, it runs on port **`8080`**.
* The server binds exclusively to **`127.0.0.1`** (localhost). It is **not** accessible from the network, maintaining the strict airgapped and private nature of the project.

### Authentication (required)

Binding to `127.0.0.1` does **not** make the server safe on its own: any web page you visit could otherwise call `http://127.0.0.1:8080` from its own JavaScript and read or write your entire store. So every route **except `GET /health`** requires a bearer token.

* On startup, `ltm serve` prints the token:

  ```
  REST server listening on http://127.0.0.1:8080
  Auth token: 9f3c…
    (source: ~/.long-term-memory/serve-token — paste it into the Web Clipper extension)
  ```

* The token is generated once and persisted `0600` at `<db-dir>/serve-token` (next to `memories.db`), so it is stable across restarts. Pin your own with the `LTM_SERVE_TOKEN` environment variable.
* Send it on every request as `X-LTM-Token: <token>` (or `Authorization: Bearer <token>`). A missing or wrong token returns `401`.
* **CORS** is returned only for `chrome-extension://` / `moz-extension://` origins — arbitrary web pages are refused by the browser even before the token check.

### API Endpoints

All payloads and responses are encoded in JSON. All endpoints except `GET /health` require the `X-LTM-Token` header.

#### `GET /health`
Returns a simple status indicating the server is alive. **No token required.**
* **Response:** `{"status":"ok"}`

#### `GET /memories`
List or search memories.
* **Query Parameters:**
  * `scope` (optional): Filter by scope (e.g. `user`, `repo:my-project`).
  * `q` (optional): Query string to run FTS5 / hybrid search.
  * `limit` (optional): Maximum number of results to return (default 200).
* **Response:** Array of memory objects (expanded records including `value`).

#### `GET /memories/{key}`
Retrieve a specific memory by its key.
* **Query Parameters:**
  * `scope` (optional): Filter by scope.
* **Response:** The requested memory object, or `404 Not Found`.

#### `POST /memories`
Create or update (upsert) a memory.
* **Request Body:**
  ```json
  {
    "scope": "web",
    "key": "my-key",
    "value": "My memory content",
    "tags": "tag1,tag2",
    "origin": "https://example.com",
    "origin_agent": "my-script"
  }
  ```
* **Response:** `201 Created` with the saved memory object.

#### `DELETE /memories/{key}`
Remove a memory.
* **Query Parameters:**
  * `scope` (optional): Filter by scope.
* **Response:** `{"deleted": true}` or `{"deleted": false}`.

Example call with the token:

```sh
curl -H "X-LTM-Token: $LTM_SERVE_TOKEN" http://127.0.0.1:8080/memories?scope=web
```

---

## 2. Browser Extension (Web Clipper)

Located in the `extension-browser/` folder of the repository, this is a lightweight Manifest V3 browser extension designed to capture web content offline.

### Features
* **Manual Note Popup:** Click the extension icon to open a sleek dark-mode popup, type notes, assign tags/scope, and save them. It automatically pulls the page URL as the origin.
* **Text Selection Clipper:** Highlight any text on a web page, right-click, and choose **"Save selection to l0-memory"**. The text will be clipped instantly in the background (the toolbar badge flashes ✓ on success, ! on failure). It auto-generates a unique key from the page title.
* Clips default to scope **`web`**, not `user`, so they never pollute the persona scope your recall hook injects into every session.

### Installation

Since the extension runs locally and is not hosted in public cloud stores to maintain privacy:

1. Start the server: `ltm serve`. Copy the **auth token** it prints.
2. Open your browser's Extension page (e.g., `chrome://extensions` in Chrome/Brave/Edge or `about:debugging` in Firefox).
3. Enable **Developer mode** (usually a toggle in the top-right).
4. Click **Load unpacked** (or "Load Temporary Add-on" in Firefox).
5. Select the `extension-browser` directory from your cloned `l0-memory` repository.
6. Click the 🧠 toolbar icon and paste the token into the **Server token** field (stored locally in `chrome.storage.local`, so you only do this once).

Then clip via the popup or the right-click menu. If something fails, the extension tells you why — "is `ltm serve` running?" on a network error, "invalid token" on `401`. Run `ltm doctor` for a one-shot check of the whole setup.
