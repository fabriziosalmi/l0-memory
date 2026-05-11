# CLI Reference

The `ltm` binary provides a comprehensive CLI for managing memories.

## Global Options

- `--scope <name>`: Set the active scope for the command.
- `LTM_SCOPE`: Environment variable to set the default scope.
- `LTM_DB`: Environment variable to override the SQLite database path.

## Commands

### `list`
List memories, pinned first, archived hidden.
```sh
ltm list [limit]
```

### `pinned`
List only pinned entries.
```sh
ltm pinned [limit]
```

### `get`
Retrieve the full record for a key.
```sh
ltm get <key>
```

### `search`
Search memories using FTS5.
```sh
ltm search <query> [limit]
```

### `save`
Insert or update a memory.
```sh
ltm save <key> <value|-> [tags]
```
*Use `-` to read the value from stdin.*

### `delete`
Remove a memory and its incident links.
```sh
ltm delete <key>
```

### `rename`
Atomic rename of a key within a scope. Cascades through links.
```sh
ltm rename <old_key> <new_key>
```

### `verify`
Mark a memory as "still current".
```sh
ltm verify <key>
```

### `supersede`
Archive an old memory and link it to a new one.
```sh
ltm supersede <old> <new> <value|-> [tags]
```

### `pin` / `unpin`
Toggle the pinned flag for a memory.
```sh
ltm pin <key>
ltm unpin <key>
```

### `link` / `unlink`
Manage directional, typed edges between memories.
```sh
ltm link <from_key> <rel> <to_key>
ltm unlink <from_key> <rel> <to_key>
```

### `traverse`
View the knowledge graph starting from a node.
```sh
ltm traverse <key> [depth]
```

### `path`
Print the path to the SQLite database.
```sh
ltm path
```
