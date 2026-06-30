# Integration: Git Hooks

**l0-memory** provides local Git hook scripts that automatically capture project development events (like commits) and write them directly into the repository scope in the memory database.

This keeps your assistant updated on recent code changes, commit history, and release updates without any manual effort.

---

## How It Works

1. Every time you run `git commit` in your workspace, the **`post-commit`** hook runs in the background.
2. The hook extracts:
   * The short commit hash (SHA)
   * The full commit message
   * The directory name of the git workspace (used as the repository name)
3. It makes a CLI call:
   ```sh
   ltm --scope repo:<repo_name> save commit-<sha> "<commit_message>" "git,commit"
   ```
4. This adds a memory to the `repo:<repo_name>` scope. The conflict resolution logic is automatically bypassed for git hooks (`LTM_CONFLICT_DISABLE=1` is injected internally) because commit history represents linear logs that should not be superseded or deleted.

---

## Installation

The Git hook assets are located in the [integrations/git/](file:///Users/fab/Documents/git/l0-memory/integrations/git/) folder.

To install the hook in your current repository:

1. Navigate to the root directory of your project.
2. Ensure you have the `ltm` binary installed and accessible (in your `PATH` or at `~/.local/bin/ltm`).
3. Make the installer script executable and run it:
   ```sh
   chmod +x integrations/git/install-hooks.sh
   ./integrations/git/install-hooks.sh
   ```

The script will copy the `post-commit` executable template to your local `.git/hooks/` directory and configure it.

### Multi-Project Usage
You can install this hook on any git repository on your machine. Simply copy the `install-hooks.sh` and `post-commit` script template into that repository and execute it. `ltm` will partition memories automatically using the repository directory name.
