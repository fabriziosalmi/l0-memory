import * as vscode from "vscode";
import { execFile, spawn, ChildProcess } from "child_process";
import * as path from "path";
import * as fs from "fs";

interface Memory {
  id: number;
  scope: string;
  key: string;
  value: string;
  tags: string;
  pinned: boolean;
  created_at: number;
  updated_at: number;
}

let mcpProcess: ChildProcess | undefined;
let provider: MemoryTreeProvider;
let pinnedProvider: PinnedTreeProvider;
let outputChannel: vscode.OutputChannel;
let statusBar: vscode.StatusBarItem | undefined;
let extensionContext: vscode.ExtensionContext;

function refreshAllViews() {
  provider?.refresh();
  pinnedProvider?.refresh();
  void updateStatusBar();
}

async function updateStatusBar() {
  if (!statusBar || !extensionContext) return;
  try {
    const out = await runLTM(extensionContext, ["list", "1000"]);
    const memories: Memory[] = JSON.parse(out || "[]") || [];
    const total = memories.length;
    const pinned = memories.filter((m) => m.pinned).length;
    statusBar.text = `$(database) l0: ${total}${pinned > 0 ? ` ($(pinned) ${pinned})` : ""}`;
    statusBar.tooltip = new vscode.MarkdownString(
      `**l0-memory** — ${total} entries, ${pinned} pinned\n\nClick to focus the sidebar.`,
    );
    statusBar.show();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) {
      statusBar.text = `$(database) l0: $(error)`;
      statusBar.tooltip = "ltm binary not found — click for help";
    } else {
      statusBar.text = `$(database) l0: $(warning)`;
      statusBar.tooltip = `l0-memory: ${err.message}`;
    }
    statusBar.show();
  }
}

// Detect whether a memory value is valid JSON (object/array, not bare scalar).
function looksLikeJSON(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

export function activate(context: vscode.ExtensionContext) {
  extensionContext = context;
  outputChannel = vscode.window.createOutputChannel("l0-memory");
  context.subscriptions.push(outputChannel);

  provider = new MemoryTreeProvider(context);
  pinnedProvider = new PinnedTreeProvider(context);
  const treeView = vscode.window.createTreeView("l0-memory.list", {
    treeDataProvider: provider,
    showCollapseAll: false,
  });
  const pinnedView = vscode.window.createTreeView("l0-memory.pinned", {
    treeDataProvider: pinnedProvider,
    showCollapseAll: false,
  });
  context.subscriptions.push(treeView, pinnedView);

  statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  statusBar.command = "l0-memory.focusList";
  context.subscriptions.push(statusBar);
  void updateStatusBar();

  context.subscriptions.push(
    vscode.commands.registerCommand("l0-memory.refresh", () => refreshAllViews()),
    vscode.commands.registerCommand("l0-memory.focusList", () =>
      vscode.commands.executeCommand("l0-memory.list.focus"),
    ),
    vscode.commands.registerCommand("l0-memory.add", () => addMemory(context)),
    vscode.commands.registerCommand("l0-memory.search", () => searchMemory()),
    vscode.commands.registerCommand("l0-memory.clearFilter", () => provider.setFilter("")),
    vscode.commands.registerCommand("l0-memory.filterScope", () => pickScopeFilter(context)),
    vscode.commands.registerCommand("l0-memory.edit", (item: MemoryItem) => editMemory(context, item)),
    vscode.commands.registerCommand("l0-memory.openInEditor", (item: MemoryItem) => openMemoryInEditor(item)),
    vscode.commands.registerCommand("l0-memory.delete", (item: MemoryItem) => deleteMemory(context, item)),
    vscode.commands.registerCommand("l0-memory.pin", (item: MemoryItem) => pinMemory(context, item, true)),
    vscode.commands.registerCommand("l0-memory.unpin", (item: MemoryItem) => pinMemory(context, item, false)),
    vscode.commands.registerCommand("l0-memory.linkTo", (item: MemoryItem) => linkMemory(context, item)),
    vscode.commands.registerCommand("l0-memory.showNeighbors", (item: MemoryItem) => showNeighbors(context, item)),
    vscode.commands.registerCommand("l0-memory.unlinkInteractive", (item: MemoryItem) => unlinkInteractive(context, item)),
    vscode.commands.registerCommand("l0-memory.openGraph", () => GraphPanel.showGlobal(context)),
    vscode.commands.registerCommand("l0-memory.openGraphFromHere", (item: MemoryItem) => {
      if (!item || !item.memory) return;
      GraphPanel.showFromHere(context, item.memory.scope, item.memory.key);
    }),
    vscode.commands.registerCommand("l0-memory.startServer", () => startMCP(context)),
    vscode.commands.registerCommand("l0-memory.stopServer", () => stopMCP()),
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("l0-memory")) refreshAllViews();
    }),
  );

  if (vscode.workspace.getConfiguration("l0-memory").get<boolean>("autoStartMCP")) {
    startMCP(context).catch((err) => outputChannel.appendLine(`autoStart failed: ${err}`));
  }

  context.subscriptions.push({ dispose: () => stopMCP() });
}

export function deactivate() {
  stopMCP();
}

function bundledBinaryPath(extensionPath: string): string {
  const goOS = process.platform === "win32" ? "windows" : process.platform;
  const goArch = process.arch === "x64" ? "amd64" : process.arch;
  const exe = process.platform === "win32" ? "ltm.exe" : "ltm";
  return path.join(extensionPath, "bin", `${goOS}-${goArch}`, exe);
}

function resolveBinary(context: vscode.ExtensionContext): string {
  const cfg = vscode.workspace.getConfiguration("l0-memory").get<string>("binaryPath");
  if (cfg && cfg.trim()) return cfg.trim();

  const bundled = bundledBinaryPath(context.extensionPath);
  if (fs.existsSync(bundled)) return bundled;

  // Dev layout: server folder next to extension (this repo's git layout).
  const devExe = process.platform === "win32" ? "ltm.exe" : "ltm";
  const dev = path.join(context.extensionPath, "..", "server", devExe);
  if (fs.existsSync(dev)) return dev;

  // Common manual install locations.
  const home = process.env.HOME || process.env.USERPROFILE || "";
  const candidates = [
    "/usr/local/bin/ltm",
    "/opt/homebrew/bin/ltm",
    path.join(home, ".local", "bin", "ltm"),
    path.join(home, "go", "bin", "ltm"),
  ];
  for (const c of candidates) if (fs.existsSync(c)) return c;

  // Last resort: rely on PATH.
  return process.platform === "win32" ? "ltm.exe" : "ltm";
}

async function notifyBinaryMissing(message: string): Promise<void> {
  const choice = await vscode.window.showErrorMessage(
    `l0-memory: ${message}`,
    "Set binary path",
    "Open output",
  );
  if (choice === "Set binary path") {
    await vscode.commands.executeCommand("workbench.action.openSettings", "l0-memory.binaryPath");
  } else if (choice === "Open output") {
    outputChannel.show(true);
  }
}

function envWithDB(): NodeJS.ProcessEnv {
  const db = vscode.workspace.getConfiguration("l0-memory").get<string>("dbPath");
  const env = { ...process.env };
  if (db && db.trim()) env.LTM_DB = db.trim();
  return env;
}

class BinaryNotFoundError extends Error {
  constructor(public readonly binPath: string) {
    super(`ltm binary not found at '${binPath}'. Set 'l0-memory.binaryPath' or install the binary.`);
    this.name = "BinaryNotFoundError";
  }
}

function runLTM(context: vscode.ExtensionContext, args: string[], stdin?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const bin = resolveBinary(context);
    const child = execFile(bin, args, { env: envWithDB(), maxBuffer: 32 * 1024 * 1024 }, (err, stdout, stderr) => {
      if (err) {
        outputChannel.appendLine(`[ltm ${args.join(" ")}] error: ${err.message}\n${stderr}`);
        if ((err as NodeJS.ErrnoException).code === "ENOENT") {
          reject(new BinaryNotFoundError(bin));
          return;
        }
        reject(new Error(stderr || err.message));
        return;
      }
      resolve(stdout);
    });
    if (stdin !== undefined && child.stdin) {
      child.stdin.write(stdin);
      child.stdin.end();
    }
  });
}

// resolveScopeForAdd returns the scope to use for a new memory, honouring the
// `l0-memory.defaultScope` setting:
//   "user"           → always "user"
//   "ask"            → quickPick with history of existing scopes + "+ new…"
//   "repo:current"   → "repo:<workspace-folder-name>" if a folder is open, else "user"
async function resolveScopeForAdd(context: vscode.ExtensionContext): Promise<string | undefined> {
  const cfg = vscode.workspace.getConfiguration("l0-memory").get<string>("defaultScope") || "user";

  if (cfg === "user") return "user";
  if (cfg === "repo:current") {
    const folder = vscode.workspace.workspaceFolders?.[0]?.name;
    return folder ? `repo:${folder}` : "user";
  }
  // "ask"
  const known = await listKnownScopes(context);
  const items: vscode.QuickPickItem[] = [
    ...known.map((s) => ({ label: s, description: s === "user" ? "(default)" : "" })),
    { label: "$(add) New scope…", description: "type a new namespace" },
  ];
  const folder = vscode.workspace.workspaceFolders?.[0]?.name;
  if (folder && !known.includes(`repo:${folder}`)) {
    items.unshift({ label: `repo:${folder}`, description: "(current workspace)" });
  }
  const picked = await vscode.window.showQuickPick(items, { placeHolder: "Choose scope" });
  if (!picked) return undefined;
  if (picked.label.startsWith("$(add)")) {
    return await vscode.window.showInputBox({ prompt: "New scope", placeHolder: "e.g. repo:my-project" });
  }
  return picked.label;
}

async function listKnownScopes(context: vscode.ExtensionContext): Promise<string[]> {
  try {
    const out = await runLTM(context, ["list", "1000"]);
    const memories: Memory[] = JSON.parse(out || "[]") || [];
    const set = new Set<string>(memories.map((m) => m.scope || "user"));
    set.add("user");
    return Array.from(set).sort();
  } catch {
    return ["user"];
  }
}

async function addMemory(context: vscode.ExtensionContext) {
  const scope = await resolveScopeForAdd(context);
  if (scope === undefined) return;
  const key = await vscode.window.showInputBox({
    prompt: `Memory key (unique within scope '${scope}')`,
    placeHolder: "e.g. focus_areas",
  });
  if (!key) return;
  const value = await vscode.window.showInputBox({ prompt: "Memory value" });
  if (value === undefined) return;
  const tags = (await vscode.window.showInputBox({ prompt: "Tags (comma-separated, optional)" })) || "";
  try {
    await runLTM(context, ["--scope", scope, "save", key, "-", tags], value);
    refreshAllViews();
    const display = scope === "user" ? key : `${scope}/${key}`;
    vscode.window.showInformationMessage(`Saved memory '${display}'.`);
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Save failed: ${err.message}`);
  }
}

async function searchMemory() {
  const q = await vscode.window.showInputBox({
    prompt: "Search memories (empty to clear filter)",
    value: provider.currentFilter(),
  });
  if (q === undefined) return;
  provider.setFilter(q.trim());
}

async function editMemory(context: vscode.ExtensionContext, item: MemoryItem) {
  if (!item || !item.memory) return;
  const m = item.memory;
  const value = await vscode.window.showInputBox({ prompt: `Edit value for '${m.key}'`, value: m.value });
  if (value === undefined) return;
  const tags = await vscode.window.showInputBox({ prompt: "Tags", value: m.tags });
  if (tags === undefined) return;
  try {
    await runLTM(context, ["save", m.key, "-", tags], value);
    refreshAllViews();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Edit failed: ${err.message}`);
  }
}

async function openMemoryInEditor(item: MemoryItem) {
  if (!item || !item.memory) return;
  const m = item.memory;

  // JSON values get a dedicated language so VSCode gives folding +
  // syntax highlight + JSON outline for free. The metadata moves to the
  // tab title; we don't prepend a markdown header that would break parsing.
  if (looksLikeJSON(m.value)) {
    const pretty = (() => {
      try { return JSON.stringify(JSON.parse(m.value), null, 2); }
      catch { return m.value; }
    })();
    const doc = await vscode.workspace.openTextDocument({ content: pretty, language: "json" });
    await vscode.window.showTextDocument(doc, { preview: false });
    return;
  }

  const ts = formatTimestamp(m.updated_at);
  const scopeLine = m.scope && m.scope !== "user" ? `_scope:_ ${m.scope}  \n` : "";
  const body =
    `# ${m.key}${m.pinned ? "  📌" : ""}\n\n` +
    scopeLine +
    `_tags:_ ${m.tags || "—"}  \n` +
    `_updated:_ ${ts}\n\n` +
    `---\n\n` +
    `${m.value}\n`;
  const doc = await vscode.workspace.openTextDocument({ content: body, language: "markdown" });
  await vscode.window.showTextDocument(doc, { preview: false });
}

async function deleteMemory(context: vscode.ExtensionContext, item: MemoryItem) {
  if (!item || !item.memory) return;
  const choice = await vscode.window.showWarningMessage(
    `Delete memory '${item.memory.key}'?`,
    { modal: true },
    "Delete",
  );
  if (choice !== "Delete") return;
  try {
    await runLTM(context, ["delete", item.memory.key]);
    refreshAllViews();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Delete failed: ${err.message}`);
  }
}

async function linkMemory(context: vscode.ExtensionContext, item: MemoryItem) {
  if (!item || !item.memory) return;
  const from = item.memory;

  // Pick the target memory from the existing set, excluding self.
  let memories: Memory[] = [];
  try {
    const out = await runLTM(context, ["list", "1000"]);
    memories = JSON.parse(out || "[]") || [];
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    return vscode.window.showErrorMessage(`Could not list memories: ${err.message}`);
  }
  const candidates = memories.filter((m) => !(m.scope === from.scope && m.key === from.key));
  if (candidates.length === 0) {
    return vscode.window.showInformationMessage("No other memories to link to.");
  }
  const target = await vscode.window.showQuickPick(
    candidates.map((m) => ({
      label: m.scope === "user" ? m.key : `${m.scope}/${m.key}`,
      description: m.tags || "",
      detail: m.value.slice(0, 100),
      memory: m,
    })),
    { placeHolder: `Link '${from.key}' to which memory?`, matchOnDetail: true },
  );
  if (!target) return;

  const rel = await vscode.window.showInputBox({
    prompt: "Relationship label",
    placeHolder: "e.g. depends_on, see_also, contradicts, paired_with",
    validateInput: (v) => (v.trim() ? null : "Required"),
  });
  if (!rel) return;

  // Same-scope shortcut available; for cross-scope, call the MCP tool layer
  // via the binary's CLI, which only supports same-scope today. We pass
  // explicit --scope on each side via two ltm calls? No — the binary's CLI
  // requires both endpoints in the same scope. Use ltm link only when scopes
  // match, otherwise inform the user that cross-scope linking is via MCP.
  if (from.scope !== target.memory.scope) {
    vscode.window.showWarningMessage(
      "Cross-scope links are only available via MCP/tool calls. Same-scope links work from the UI.",
    );
    return;
  }
  try {
    await runLTM(context, ["--scope", from.scope, "link", from.key, rel.trim(), target.memory.key]);
    refreshAllViews();
    vscode.window.showInformationMessage(`Linked ${from.key} —${rel.trim()}→ ${target.memory.key}`);
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Link failed: ${err.message}`);
  }
}

interface Link {
  id: number;
  from_scope: string;
  from_key: string;
  to_scope: string;
  to_key: string;
  rel: string;
  created_at: number;
}

function uri(scope: string, key: string): string {
  return scope === "user" ? key : `${scope}/${key}`;
}

async function showNeighbors(context: vscode.ExtensionContext, item: MemoryItem) {
  if (!item || !item.memory) return;
  const m = item.memory;
  try {
    const out = await runLTM(context, ["--scope", m.scope, "links", m.key]);
    const links: Link[] = JSON.parse(out || "[]") || [];
    if (links.length === 0) {
      return vscode.window.showInformationMessage(`No links incident to '${m.key}'.`);
    }
    const lines = [
      `# Links incident to ${uri(m.scope, m.key)}`,
      "",
      `${links.length} edge${links.length === 1 ? "" : "s"}:`,
      "",
      ...links.map((l) => {
        const from = uri(l.from_scope, l.from_key);
        const to = uri(l.to_scope, l.to_key);
        const arrow = l.from_scope === m.scope && l.from_key === m.key ? "→" : "←";
        const other = arrow === "→" ? to : from;
        return `- **${l.rel}** ${arrow} ${other}`;
      }),
    ];
    const doc = await vscode.workspace.openTextDocument({
      content: lines.join("\n") + "\n",
      language: "markdown",
    });
    await vscode.window.showTextDocument(doc, { preview: false });
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Neighbors load failed: ${err.message}`);
  }
}

async function unlinkInteractive(context: vscode.ExtensionContext, item: MemoryItem) {
  if (!item || !item.memory) return;
  const m = item.memory;
  let links: Link[] = [];
  try {
    const out = await runLTM(context, ["--scope", m.scope, "links", m.key]);
    links = JSON.parse(out || "[]") || [];
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    return vscode.window.showErrorMessage(`Could not load links: ${err.message}`);
  }
  if (links.length === 0) {
    return vscode.window.showInformationMessage(`No links to remove from '${m.key}'.`);
  }
  const picked = await vscode.window.showQuickPick(
    links.map((l) => {
      const from = uri(l.from_scope, l.from_key);
      const to = uri(l.to_scope, l.to_key);
      return { label: `${from} —${l.rel}→ ${to}`, link: l };
    }),
    { placeHolder: "Remove which link?" },
  );
  if (!picked) return;
  const l = picked.link;
  try {
    // CLI ltm unlink only works same-scope; warn otherwise.
    if (l.from_scope !== l.to_scope) {
      vscode.window.showWarningMessage("Cross-scope unlink is only available via MCP/tool calls.");
      return;
    }
    await runLTM(context, ["--scope", l.from_scope, "unlink", l.from_key, l.rel, l.to_key]);
    refreshAllViews();
    vscode.window.showInformationMessage(`Unlinked ${l.from_key} —${l.rel}→ ${l.to_key}`);
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Unlink failed: ${err.message}`);
  }
}

// --- Graph webview ---------------------------------------------------------

interface GraphNode {
  id: string;
  scope: string;
  key: string;
  label: string;
  pinned: boolean;
  root?: boolean;
}
interface GraphEdge {
  source: string;
  target: string;
  rel: string;
}
interface GraphPayload {
  root?: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

interface TraverseGraphView {
  root: string;
  depth: number;
  nodes: { uri: string; scope: string; key: string; tags: string; pinned: boolean; depth: number }[];
  edges: { from: string; to: string; rel: string }[];
}

class GraphPanel {
  private static current: GraphPanel | undefined;
  private readonly panel: vscode.WebviewPanel;
  private readonly disposables: vscode.Disposable[] = [];
  private rootScope?: string;
  private rootKey?: string;
  private depth = 2;
  private direction: "out" | "in" | "both" = "both";

  static showFromHere(context: vscode.ExtensionContext, scope: string, key: string) {
    const p = GraphPanel.ensure(context);
    p.rootScope = scope;
    p.rootKey = key;
    p.panel.title = `l0-memory · ${scope === "user" ? key : `${scope}/${key}`}`;
    p.refresh();
  }

  static showGlobal(context: vscode.ExtensionContext) {
    const p = GraphPanel.ensure(context);
    p.rootScope = undefined;
    p.rootKey = undefined;
    p.panel.title = "l0-memory · graph";
    p.refresh();
  }

  private static ensure(context: vscode.ExtensionContext): GraphPanel {
    if (GraphPanel.current) {
      GraphPanel.current.panel.reveal(vscode.ViewColumn.Beside);
      return GraphPanel.current;
    }
    const panel = vscode.window.createWebviewPanel(
      "l0-memory.graph",
      "l0-memory · graph",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(context.extensionUri, "media")],
      },
    );
    GraphPanel.current = new GraphPanel(context, panel);
    return GraphPanel.current;
  }

  private constructor(private context: vscode.ExtensionContext, panel: vscode.WebviewPanel) {
    this.panel = panel;
    this.panel.webview.html = this.html();
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
    this.panel.webview.onDidReceiveMessage(
      (msg) => this.onMessage(msg),
      null,
      this.disposables,
    );
  }

  private dispose() {
    GraphPanel.current = undefined;
    this.panel.dispose();
    while (this.disposables.length) {
      const d = this.disposables.pop();
      if (d) d.dispose();
    }
  }

  private async onMessage(msg: { type: string; [k: string]: unknown }) {
    switch (msg.type) {
      case "ready":
        await this.refresh();
        break;
      case "reload":
        if (typeof msg.depth === "number") this.depth = msg.depth;
        if (typeof msg.direction === "string") this.direction = msg.direction as typeof this.direction;
        await this.refresh();
        break;
      case "open": {
        const scope = String(msg.scope || "");
        const key = String(msg.key || "");
        await this.openMemoryByRef(scope, key);
        break;
      }
      case "reroot": {
        const scope = String(msg.scope || "");
        const key = String(msg.key || "");
        this.rootScope = scope;
        this.rootKey = key;
        this.panel.title = `l0-memory · ${scope === "user" ? key : `${scope}/${key}`}`;
        await this.refresh();
        break;
      }
    }
  }

  private async openMemoryByRef(scope: string, key: string) {
    try {
      const out = await runLTM(this.context, ["--scope", scope, "get", key]);
      const m = JSON.parse(out || "null");
      if (!m || m.found === false) return;
      const item = MemoryItem.forMemory(m as Memory);
      await openMemoryInEditor(item);
    } catch {
      // ignore
    }
  }

  private async refresh() {
    try {
      const payload =
        this.rootScope && this.rootKey
          ? await this.buildFromTraverse(this.rootScope, this.rootKey)
          : await this.buildGlobal();
      this.panel.webview.postMessage({ type: "data", payload });
    } catch (e: unknown) {
      const err = e as Error;
      outputChannel.appendLine(`graph refresh failed: ${err.message}`);
      this.panel.webview.postMessage({
        type: "data",
        payload: { nodes: [], edges: [], root: "(error)" },
      });
    }
  }

  private async buildFromTraverse(scope: string, key: string): Promise<GraphPayload> {
    const out = await runLTM(this.context, [
      "--scope", scope, "traverse", key, String(this.depth),
    ]);
    const view = JSON.parse(out || "null") as TraverseGraphView | null;
    if (!view) return { nodes: [], edges: [] };

    const nodes: GraphNode[] = view.nodes.map((n) => ({
      id: n.uri,
      scope: n.scope,
      key: n.key,
      label: n.scope === "user" ? n.key : `${n.scope}/${n.key}`,
      pinned: n.pinned,
      root: n.depth === 0,
    }));
    const edges: GraphEdge[] = view.edges.map((e) => ({
      source: e.from,
      target: e.to,
      rel: e.rel,
    }));
    return { root: view.root, nodes, edges };
  }

  private async buildGlobal(): Promise<GraphPayload> {
    // Walk every memory and ask for its outgoing links. With small datasets
    // this is cheap; for very large stores we'd want a `ltm export` dump.
    const listOut = await runLTM(this.context, ["list", "1000"]);
    const memories: Memory[] = JSON.parse(listOut || "[]") || [];
    const nodeMap = new Map<string, GraphNode>();
    for (const m of memories) {
      const id = `memory:///${encodeURIComponent(m.scope)}/${encodeURIComponent(m.key)}`;
      nodeMap.set(id, {
        id,
        scope: m.scope || "user",
        key: m.key,
        label: m.scope && m.scope !== "user" ? `${m.scope}/${m.key}` : m.key,
        pinned: m.pinned,
      });
    }
    const edges: GraphEdge[] = [];
    const seenEdge = new Set<string>();
    for (const m of memories) {
      const linksOut = await runLTM(this.context, ["--scope", m.scope || "user", "links", m.key]);
      const links: Link[] = JSON.parse(linksOut || "[]") || [];
      for (const l of links) {
        const fromId = `memory:///${encodeURIComponent(l.from_scope)}/${encodeURIComponent(l.from_key)}`;
        const toId = `memory:///${encodeURIComponent(l.to_scope)}/${encodeURIComponent(l.to_key)}`;
        const sig = `${fromId}|${l.rel}|${toId}`;
        if (seenEdge.has(sig)) continue;
        seenEdge.add(sig);
        // Only emit edges whose endpoints we actually have nodes for; the
        // FK cascade should keep this consistent, but be defensive.
        if (nodeMap.has(fromId) && nodeMap.has(toId)) {
          edges.push({ source: fromId, target: toId, rel: l.rel });
        }
      }
    }
    return { nodes: Array.from(nodeMap.values()), edges };
  }

  private html(): string {
    const webview = this.panel.webview;
    const mediaUri = (file: string) =>
      webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, "media", file));
    const html = fs.readFileSync(
      path.join(this.context.extensionPath, "media", "graph.html"),
      "utf8",
    );
    return html
      .replaceAll("${cssUri}", mediaUri("graph.css").toString())
      .replaceAll("${jsUri}", mediaUri("graph.js").toString())
      .replaceAll("${d3Uri}", mediaUri("d3.v7.min.js").toString())
      .replaceAll("${cspSource}", webview.cspSource);
  }
}

async function pinMemory(context: vscode.ExtensionContext, item: MemoryItem, pin: boolean) {
  if (!item || !item.memory) return;
  try {
    const args = ["--scope", item.memory.scope || "user", pin ? "pin" : "unpin", item.memory.key];
    await runLTM(context, args);
    refreshAllViews();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`${pin ? "Pin" : "Unpin"} failed: ${err.message}`);
  }
}

async function startMCP(context: vscode.ExtensionContext) {
  if (mcpProcess && !mcpProcess.killed) {
    vscode.window.showInformationMessage("MCP server already running.");
    return;
  }
  const bin = resolveBinary(context);
  mcpProcess = spawn(bin, ["mcp"], { env: envWithDB(), stdio: ["pipe", "pipe", "pipe"] });
  mcpProcess.stdout?.on("data", (d) => outputChannel.append(`[mcp.out] ${d}`));
  mcpProcess.stderr?.on("data", (d) => outputChannel.append(`[mcp.err] ${d}`));
  mcpProcess.on("exit", (code) => {
    outputChannel.appendLine(`[mcp] exited with code ${code}`);
    mcpProcess = undefined;
  });
  outputChannel.appendLine(`[mcp] started ${bin}`);
}

function stopMCP() {
  if (mcpProcess && !mcpProcess.killed) {
    mcpProcess.kill();
    mcpProcess = undefined;
  }
}

// Server stores Unix ms; older builds emitted seconds, so values < ~Sep 2001
// in ms are rescaled.
function formatTimestamp(t: number): string {
  const ms = t < 1e12 ? t * 1000 : t;
  return new Date(ms).toLocaleString();
}

class MemoryTreeProvider implements vscode.TreeDataProvider<MemoryItem> {
  private _onDidChange = new vscode.EventEmitter<MemoryItem | undefined | void>();
  readonly onDidChangeTreeData = this._onDidChange.event;
  private filter = "";
  private scopeFilter = ""; // "" means all scopes

  constructor(private context: vscode.ExtensionContext) {}

  refresh() { this._onDidChange.fire(); }
  setFilter(q: string) { this.filter = q; this.refresh(); }
  currentFilter(): string { return this.filter; }
  setScopeFilter(s: string) { this.scopeFilter = s; this.refresh(); }
  currentScopeFilter(): string { return this.scopeFilter; }

  getTreeItem(el: MemoryItem) { return el; }

  async getChildren(): Promise<MemoryItem[]> {
    try {
      const baseArgs = this.scopeFilter ? ["--scope", this.scopeFilter] : [];
      const cmd = this.filter ? ["search", this.filter, "200"] : ["list", "200"];
      const out = await runLTM(this.context, [...baseArgs, ...cmd]);
      const memories: Memory[] = JSON.parse(out || "[]") || [];
      if (memories.length === 0) {
        const scopeNote = this.scopeFilter ? ` in scope '${this.scopeFilter}'` : "";
        const label = this.filter
          ? `No matches for "${this.filter}"${scopeNote}`
          : `No memories${scopeNote} yet — click + to add one`;
        return [MemoryItem.placeholder(label)];
      }
      return memories.map((m) => MemoryItem.forMemory(m));
    } catch (e: unknown) {
      const err = e as Error;
      outputChannel.appendLine(`tree load failed: ${err.message}`);
      if (err instanceof BinaryNotFoundError) {
        void notifyBinaryMissing(err.message);
        return [MemoryItem.placeholder("ltm binary not found — see notification")];
      }
      return [MemoryItem.placeholder(`Error: ${err.message}`)];
    }
  }
}

async function pickScopeFilter(context: vscode.ExtensionContext) {
  const known = await listKnownScopes(context);
  const items: vscode.QuickPickItem[] = [
    { label: "$(globe) All scopes", description: "no filter" },
    ...known.map((s) => ({ label: s, description: s === "user" ? "(default)" : "" })),
  ];
  const current = provider?.currentScopeFilter() || "(all)";
  const picked = await vscode.window.showQuickPick(items, {
    placeHolder: `Filter by scope (current: ${current})`,
  });
  if (!picked) return;
  provider.setScopeFilter(picked.label.startsWith("$(globe)") ? "" : picked.label);
}

class PinnedTreeProvider implements vscode.TreeDataProvider<MemoryItem> {
  private _onDidChange = new vscode.EventEmitter<MemoryItem | undefined | void>();
  readonly onDidChangeTreeData = this._onDidChange.event;

  constructor(private context: vscode.ExtensionContext) {}

  refresh() { this._onDidChange.fire(); }

  getTreeItem(el: MemoryItem) { return el; }

  async getChildren(): Promise<MemoryItem[]> {
    try {
      const out = await runLTM(this.context, ["pinned", "200"]);
      const memories: Memory[] = JSON.parse(out || "[]") || [];
      if (memories.length === 0) {
        return [MemoryItem.placeholder("No pinned memories — right-click an entry to pin it")];
      }
      return memories.map((m) => MemoryItem.forMemory(m));
    } catch (e: unknown) {
      const err = e as Error;
      outputChannel.appendLine(`pinned tree load failed: ${err.message}`);
      if (err instanceof BinaryNotFoundError) {
        return [MemoryItem.placeholder("ltm binary not found")];
      }
      return [MemoryItem.placeholder(`Error: ${err.message}`)];
    }
  }
}

class MemoryItem extends vscode.TreeItem {
  readonly memory?: Memory;

  private constructor(label: string, memory?: Memory) {
    super(label, vscode.TreeItemCollapsibleState.None);
    this.memory = memory;
  }

  static forMemory(m: Memory): MemoryItem {
    // Default scope is "user"; show non-default scopes in front of the key
    // so the same key in different scopes is visually distinguishable.
    const label = m.scope && m.scope !== "user" ? `${m.scope}/${m.key}` : m.key;
    const item = new MemoryItem(label, m);
    const preview = m.value.length > 80 ? m.value.slice(0, 77) + "..." : m.value;
    item.description = preview;
    const ts = formatTimestamp(m.updated_at);
    const pinTag = m.pinned ? "  📌" : "";
    item.tooltip = new vscode.MarkdownString(
      `**${label}**${pinTag}\n\n${m.value}\n\n_scope:_ ${m.scope || "user"}  \n_tags:_ ${m.tags || "—"}  \n_pinned:_ ${m.pinned ? "yes" : "no"}  \n_updated:_ ${ts}`,
    );
    // contextValue drives the menu/visibility for pin vs unpin actions.
    item.contextValue = m.pinned ? "memory.pinned" : "memory.unpinned";
    item.iconPath = new vscode.ThemeIcon(m.pinned ? "pinned" : "symbol-key");
    item.command = {
      command: "l0-memory.openInEditor",
      title: "Open memory",
      arguments: [item],
    };
    return item;
  }

  static placeholder(label: string): MemoryItem {
    const item = new MemoryItem(label);
    item.iconPath = new vscode.ThemeIcon("info");
    return item;
  }
}
