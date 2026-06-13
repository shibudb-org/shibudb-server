## ShibuDb Troubleshooting

## Server won’t start

- **Port conflict**: if `--port` or `--management-port` is already in use, choose different ports.

```bash
shibudb start --port 9090 --management-port 19090
```

- **Server already running**: `shibudb start` uses a PID file under `<data-dir>/run/shibudb.pid`.

```bash
shibudb stop
```

If you used a custom data directory:

```bash
shibudb stop --data-dir /path/to/data
```

## Can’t log in / lost admin password

Credentials are stored in `<data-dir>/lib/users.json`. If you remove that file, the next `shibudb start` will prompt you to create a new admin user (or you can bootstrap with flags).

```bash
rm ~/.shibudb/lib/users.json
shibudb start
```

## Management API returns 403 Forbidden

The management API requires a bearer token:

```text
Authorization: Bearer <token>
```

Create a token (admin-only):

```bash
shibudb manager --username <admin> --password <pass> generate-token
```

## `DELETE-VECTOR` fails for HNSW

HNSW index types do not support vector deletion. Use Flat / IVF / PQ index types if you need deletes.

## “vector dimension mismatch”

Your query vector must have exactly `<space dimension>` values. Recreate the space with the correct dimension or send the correct-length vector.

## Metadata filtering (`--meta` / `--where`) errors

- **“metadata filtering is only supported for Flat spaces declared with indexed metadata fields”**: the space was not created with `--index-type Flat` and `--metadata-fields`. Recreate it with both.
- **“indexed metadata fields are only supported for the Flat index type”**: you passed `--metadata-fields` with a non-Flat `--index-type`. Use `--index-type Flat`.
- **“filter field "X" is not an indexed metadata field”**: `X` was not declared in `--metadata-fields`. Only declared fields can be filtered.
- **“range op "gt"/"lt"/... on "X" requires a number”**: a comparison/`BETWEEN` was used on a string value. Use a numeric (`int`/`float`) field, or quote string equality (`field='value'`).
- **No results when you expect some**: a string field whose value looks numeric may have been stored/queried as a number. Quote it (e.g. `user_id='123'`). Also remember `--meta`/`--metadata-fields` must not contain spaces.

See [Vector Engine — Metadata Filtering](VECTOR_ENGINE.md#metadata-filtering) for the full grammar.

## Where are my files?

Default data directory root:

- If `XDG_DATA_HOME` is set: `$XDG_DATA_HOME/shibudb`
- Else: `~/.shibudb`

Layout:

```text
<data-dir>/
  lib/
  log/shibudb.log
  run/shibudb.pid
```

