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
* It sets CORS headers allowing local origins (such as browser extensions).

### API Endpoints

All payloads and responses are encoded in JSON.

#### `GET /health`
Returns a simple status indicating the server is alive.
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
    "scope": "user",
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

---

## 2. Browser Extension (Web Clipper)

Located in the [extension-browser/](file:///Users/fab/Documents/git/l0-memory/extension-browser/) folder, this is a lightweight Manifest V3 browser extension designed to capture web content offline.

### Features
* **Manual Note Popup:** Click the extension icon to open a sleek dark-mode popup, type notes, assign tags/scope, and save them. It automatically pulls the page URL as the origin.
* **Text Selection Clipper:** Highlight any text on a web page, right-click, and choose **"Save selection to l0-memory"**. The text will be clipped instantly in the background. It auto-generates a unique key from the page title.

### Installation

Since the extension runs locally and is not hosted in public cloud stores to maintain privacy:
1. Open your browser's Extension page (e.g., `chrome://extensions` in Chrome/Brave/Edge or `about:debugging` in Firefox).
2. Enable **Developer mode** (usually a toggle in the top-right).
3. Click **Load unpacked** (or "Load Temporary Add-on" in Firefox).
4. Select the `extension-browser` directory from your cloned `l0-memory` repository.
5. Ensure your local server is running by executing `ltm serve` in your terminal.
