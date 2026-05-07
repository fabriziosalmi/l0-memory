import * as vscode from "vscode";
import { execFile, spawn, ChildProcess } from "child_process";
import * as path from "path";
import * as fs from "fs";

interface Memory {
  id: number;
  key: string;
  value: string;
  tags: string;
  created_at: number;
  updated_at: number;
}

let mcpProcess: ChildProcess | undefined;
let provider: MemoryTreeProvider;
let outputChannel: vscode.OutputChannel;

export function activate(context: vscode.ExtensionContext) {
  outputChannel = vscode.window.createOutputChannel("l0-memory");
  context.subscriptions.push(outputChannel);

  provider = new MemoryTreeProvider(context);
  const treeView = vscode.window.createTreeView("l0-memory.list", {
    treeDataProvider: provider,
    showCollapseAll: false,
  });
  context.subscriptions.push(treeView);

  context.subscriptions.push(
    vscode.commands.registerCommand("l0-memory.refresh", () => provider.refresh()),
    vscode.commands.registerCommand("l0-memory.add", () => addMemory(context)),
    vscode.commands.registerCommand("l0-memory.search", () => searchMemory()),
    vscode.commands.registerCommand("l0-memory.clearFilter", () => provider.setFilter("")),
    vscode.commands.registerCommand("l0-memory.edit", (item: MemoryItem) => editMemory(context, item)),
    vscode.commands.registerCommand("l0-memory.openInEditor", (item: MemoryItem) => openMemoryInEditor(item)),
    vscode.commands.registerCommand("l0-memory.delete", (item: MemoryItem) => deleteMemory(context, item)),
    vscode.commands.registerCommand("l0-memory.startServer", () => startMCP(context)),
    vscode.commands.registerCommand("l0-memory.stopServer", () => stopMCP()),
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("l0-memory")) provider.refresh();
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

async function addMemory(context: vscode.ExtensionContext) {
  const key = await vscode.window.showInputBox({ prompt: "Memory key (unique)", placeHolder: "e.g. user_role" });
  if (!key) return;
  const value = await vscode.window.showInputBox({ prompt: "Memory value" });
  if (value === undefined) return;
  const tags = (await vscode.window.showInputBox({ prompt: "Tags (comma-separated, optional)" })) || "";
  try {
    await runLTM(context, ["save", key, "-", tags], value);
    provider.refresh();
    vscode.window.showInformationMessage(`Saved memory '${key}'.`);
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
    provider.refresh();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Edit failed: ${err.message}`);
  }
}

async function openMemoryInEditor(item: MemoryItem) {
  if (!item || !item.memory) return;
  const m = item.memory;
  const ts = formatTimestamp(m.updated_at);
  const body =
    `# ${m.key}\n\n` +
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
    provider.refresh();
  } catch (e: unknown) {
    const err = e as Error;
    if (err instanceof BinaryNotFoundError) return notifyBinaryMissing(err.message);
    vscode.window.showErrorMessage(`Delete failed: ${err.message}`);
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

  constructor(private context: vscode.ExtensionContext) {}

  refresh() { this._onDidChange.fire(); }
  setFilter(q: string) { this.filter = q; this.refresh(); }
  currentFilter(): string { return this.filter; }

  getTreeItem(el: MemoryItem) { return el; }

  async getChildren(): Promise<MemoryItem[]> {
    try {
      const args = this.filter ? ["search", this.filter, "200"] : ["list", "200"];
      const out = await runLTM(this.context, args);
      const memories: Memory[] = JSON.parse(out || "[]") || [];
      if (memories.length === 0) {
        const label = this.filter
          ? `No matches for "${this.filter}" — run "l0-memory: Clear search filter"`
          : "No memories yet — click + to add one";
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

class MemoryItem extends vscode.TreeItem {
  readonly memory?: Memory;

  private constructor(label: string, memory?: Memory) {
    super(label, vscode.TreeItemCollapsibleState.None);
    this.memory = memory;
  }

  static forMemory(m: Memory): MemoryItem {
    const item = new MemoryItem(m.key, m);
    const preview = m.value.length > 80 ? m.value.slice(0, 77) + "..." : m.value;
    item.description = preview;
    const ts = formatTimestamp(m.updated_at);
    item.tooltip = new vscode.MarkdownString(
      `**${m.key}**\n\n${m.value}\n\n_tags:_ ${m.tags || "—"}  \n_updated:_ ${ts}`,
    );
    item.contextValue = "memory";
    item.iconPath = new vscode.ThemeIcon("symbol-key");
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
