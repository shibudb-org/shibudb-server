## ShibuDb API Reference

This reference documents the CLI commands and management endpoints that exist in the current codebase.

## CLI entrypoints

```text
shibudb start [flags]
shibudb stop [--data-dir <path>]
shibudb connect [--port <n>] [--username <u> --password <p>]
shibudb manager [--port <n>] [--data-dir <path>] [--username <u> --password <p>] <command> [args...]
shibudb rebuild-index [--data-dir <path>] <space_name>
```

## `shibudb start` flags

```text
--data-dir <path>           Data directory root (default: $XDG_DATA_HOME/shibudb or ~/.shibudb)
--admin-user <u>            Admin username for initial bootstrap (non-interactive)
--admin-password <p>        Admin password for initial bootstrap (non-interactive)
--port <n>                  TCP port for client connections (default: 4444)
--management-port <n>       TCP port for management HTTP API (default: 5444; must differ from --port)
--max-connections <n>       Maximum concurrent connections (default: 1000, or SHIBUDB_MAX_CONNECTIONS)
```

## `shibudb connect` flags

```text
--port <n>                  TCP port of the ShibuDb server (default: 4444)
--username <u>              Optional; prompts if omitted (alias: --user)
--password <p>              Optional; prompts if omitted (alias: --pass)
```

## Interactive database commands (inside `shibudb connect`)

Commands are case-insensitive. Type `HELP` (or `?`) at the prompt to print the full command reference.

### General

```text
HELP, ?                          Print the interactive command reference
EXIT, QUIT                       Disconnect and exit
```

### Space management

```text
USE <space>
LIST-SPACES
CREATE-SPACE <name> [--engine key-value|vector] [--dimension N] [--index-type TYPE] [--metric METRIC] [--enable-wal] [--disable-wal] [--segment-rollover-bytes N] [--max-segments-before-merge N] [--metadata-fields name:type,...]
DELETE-SPACE <name>
```

Notes:
- `CREATE-SPACE ... --engine vector` requires `--dimension N`.
- WAL is **disabled by default** unless `--enable-wal` is provided.
- `--metadata-fields` declares indexed metadata fields for filtering and is **only valid with `--index-type Flat`**. Format is comma-separated `name:type` (no spaces); `type` is `string`, `int`, or `float`. See [Metadata Filtering](VECTOR_ENGINE.md#metadata-filtering).

### Key-value operations (require `USE <space>` on a key-value space)

```text
PUT <key> <value>
GET <key>
DELETE <key>
```

### Vector operations (require `USE <space>` on a vector space)

Vector IDs must be numeric (parsed as `int64`).

```text
INSERT-VECTOR <id> <comma-separated-floats> [--meta key=value,...]
DELETE-VECTOR <id>
GET-VECTOR <id>
SEARCH-TOPK <comma-separated-floats> <k> [--where <expression>]
RANGE-SEARCH <comma-separated-floats> <radius> [--where <expression>]
```

Notes:
- Query vector length must match the space dimension.
- `DELETE-VECTOR` is not supported for HNSW index types.
- `--meta` and `--where` are only available on `Flat` spaces created with `--metadata-fields`.
  `--meta` is comma-separated `key=value` (no spaces); numeric values are inferred, quote to force a string.
- `--where` is a boolean filter expression supporting `= != > >= < <=`, `IN (...)`,
  `BETWEEN low AND high`, `AND`/`OR`/`NOT`, and parentheses. Full grammar and examples:
  [Metadata Filtering](VECTOR_ENGINE.md#metadata-filtering).

### User management commands (admin-only)

```text
CREATE-USER
UPDATE-USER-PASSWORD <username>
UPDATE-USER-ROLE <username>
UPDATE-USER-PERMISSIONS <username>
DELETE-USER <username>
GET-USER <username>
```

## `shibudb manager` commands

The manager talks to the management HTTP API (`--management-port`, default 5444).

```text
status
stats
health
limit <new_limit>
increase [amount]         (default amount: 100)
decrease [amount]         (default amount: 100)
reset
update-space-settings --segment-rollover-bytes <bytes> --max-segments-before-merge <n> <space>
generate-token
list-tokens
delete-token <token_id>
```

## Management HTTP API endpoints

All endpoints require:

```text
Authorization: Bearer <token>
```

Endpoints:

```text
GET  /health
GET  /stats
GET  /limit
PUT  /limit
POST /limit/increase
POST /limit/decrease
PUT  /spaces/settings
```

