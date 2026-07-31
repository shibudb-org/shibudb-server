## ShibuDb Administration Guide

This guide covers server administration tasks that are implemented in the current codebase.

## Starting and stopping the server

```bash
# Start server as a background process
shibudb start

# Stop server started via `shibudb start`
shibudb stop
```

### Common flags

```bash
# Override data directory root (defaults to $XDG_DATA_HOME/shibudb or ~/.shibudb)
shibudb start --data-dir /path/to/data

# Set client + management ports (must differ)
shibudb start --port 9090 --management-port 19090

# Bootstrap admin non-interactively on first startup
shibudb start --admin-user admin --admin-password admin

# Set initial connection limit
shibudb start --max-connections 2000
```

## Data directory layout

ShibuDb stores runtime files under a single data directory root:

```text
<data-dir>/
  lib/
    users.json
    management_tokens.json
    connection_limit.json
    <space>/
      space.meta.json
      data.db / wal.db / index.dat                 (key-value)
      vector_data.db / vector_wal.db / vector_index.faiss  (vector)
  log/
    shibudb.log
  run/
    shibudb.pid
```

Notes:
- `connection_limit.json` is used to persist the last saved connection limit across restarts.
- Each space has its own directory under `lib/`.

## Management API (connection limits + space settings)

The server exposes an HTTP management API on `--management-port` (default `5444`). All endpoints require a bearer token:

```text
Authorization: Bearer <token>
```

### Tokens

Tokens are stored in `<data-dir>/lib/management_tokens.json`.

```bash
# Create a long-lived token (admin-only)
shibudb manager --username <admin> --password <pass> generate-token

# List stored tokens
shibudb manager --username <admin> --password <pass> list-tokens

# Delete a stored token
shibudb manager --username <admin> --password <pass> delete-token <token_id>
```

### HTTP endpoints

```bash
# Health
curl http://localhost:5444/health -H "Authorization: Bearer <token>"

# Stats
curl http://localhost:5444/stats -H "Authorization: Bearer <token>"

# Get current limit
curl http://localhost:5444/limit -H "Authorization: Bearer <token>"

# Set limit
curl -X PUT http://localhost:5444/limit \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"limit": 2000}'
```

### CLI management (recommended)

The `shibudb manager` command talks to the management API.

```bash
shibudb manager --username <admin> --password <pass> status
shibudb manager --username <admin> --password <pass> stats
shibudb manager --username <admin> --password <pass> limit 2000
shibudb manager --username <admin> --password <pass> increase 500
shibudb manager --username <admin> --password <pass> decrease 200
shibudb manager --username <admin> --password <pass> health
```

### Space settings updates

The management API also supports updating space settings:

- CLI: `shibudb manager ... update-space-settings --segment-rollover-bytes N --max-segments-before-merge N <space>`
- HTTP: `PUT /spaces/settings`

Important behavior:
- Segment rollover / merge settings apply to **Flat** and **HNSW** vector index types (segmented vector storage).
- For training-based vector index types (**IVF**, **PQ**, …), segment settings do not apply.

## Rebuilding a space index

When the server is stopped, you can rebuild a space index from on-disk data:

```bash
shibudb rebuild-index <space_name>
```

If you use a non-default data directory:

```bash
shibudb rebuild-index --data-dir /path/to/data <space_name>
```

## Database Backup and Restore (Dump & Restore)

ShibuDB includes native offline dump and restore commands to export database spaces to portable JSON-Lines (JSONL) files and restore them later.

> **Note**: The server must be stopped before running `dump` or `restore` to prevent inconsistent reads or concurrent file writes.

### Dumping (Exporting)

Export the entire database (all spaces, metadata, and live records) to a dump file:

```bash
shibudb dump --output backup.jsonl
```

Export a specific space only:

```bash
shibudb dump --space my_space --output my_space.jsonl
```

Dump flags:
- `--data-dir <path>`: Root data directory (default: `~/.shibudb`)
- `--output <file>`: Output file path (default: stdout)
- `--space <name>`: Dump only this space (default: all spaces)

### Restoring (Importing)

Restore database spaces from a dump file (full restore, overwriting existing data):

```bash
shibudb restore --input backup.jsonl
```

Restore and merge dump records into existing spaces (dump records take precedence on key conflicts):

```bash
shibudb restore --input backup.jsonl --mode merge
```

Restore flags:
- `--data-dir <path>`: Root data directory (default: `~/.shibudb`)
- `--input <file>`: Input dump file path (default: stdin)
- `--mode overwrite|merge`: Restore mode — `overwrite` (default) replaces existing spaces; `merge` overlays dump data


