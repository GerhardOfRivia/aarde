# Configuration and deployment

[Documentation index](README.md) · [Project README](../README.md)

## Docker and configuration

Compose starts exactly two services: `aarde` and `postgres`. Postgres is not exposed on the host. The web port binds to `127.0.0.1:8080`. Imagery is mounted read-only at `/data`; the application runs as UID 10001. Give that user read access to imagery and traversal access to its directories.

```sh
# Run from the repository root.
cp .env.example .env
docker compose up -d --build
docker compose logs -f aarde
docker compose exec aarde aarde version
docker compose down             # keeps the catalog volume
```

See [`.env.example`](../.env.example) for a starting configuration. `.env` is read by Compose. The Go binary reads its process environment, so export variables when running locally. Compose supplies its own internal database URL and listen address.

| Variable | Default / purpose |
| --- | --- |
| `AARDE_DATABASE_URL` | Required for server, non-dry import, CLI search, and removal; PostgreSQL URL |
| `AARDE_LISTEN_ADDRESS` | `:8080` |
| `AARDE_LOG_LEVEL` | `info`; also `debug`, `warn`, `error` |
| `AARDE_WEB_PUBLIC_READ` | `false`; allow anonymous catalog browsing and search |
| `AARDE_WEB_BASEMAP` | Native default `osm`; Compose default `offline`. Use `offline` for the bundled map or `none` for a plain background; both prevent external tiles for all browsers |
| `AARDE_WEB_TOKEN_PATH` | Optional private token file; see below |
| `POSTGRES_DB`, `POSTGRES_USER` | Compose only; both default to `aarde` |
| `POSTGRES_PASSWORD` | Compose only; `aarde-local` for local development |

Change the example database password for shared deployments. URL-encode special characters in connection URLs. The web API requires the startup bearer token by default. Use HTTPS at a reverse proxy or a trusted local connection to protect it in transit. Multi-user identity management belongs at the proxy. CORS is intentionally same-origin; development uses Vite's API proxy.

Viewer rendering budgets, concurrency, timeout, scratch space, and source-root
restrictions use `AARDE_VIEWER_*` settings. See the
[viewer configuration reference](image-viewer.md#rendering-security-and-resource-configuration),
including Compose's memory, CPU, and temporary-storage limits.

## Offline deployments

For a network without internet, set `AARDE_WEB_BASEMAP=offline` in Compose's `.env` and rebuild/recreate the application with `docker compose up -d --build aarde`. This is also Compose's default when the variable is unset. For a native deployment, rebuild the frontend and Go binary, then set the variable in the server process environment before starting `aarde serve`. This forces **Offline map** for every browser, including first visits and browsers with a saved OpenStreetMap preference; the basemap selector is disabled. `AARDE_WEB_BASEMAP=none` similarly forces **No basemap**. Neither mode makes external tile requests. Prepare the application/database images and dependencies before moving to the offline network.

The bundled overview map and browser basemap controls are described in the
[user guide](user-guide.md#appearance-and-basemaps). Normal builds use checked-in
basemap data without downloading it.

## Database migrations and backups

Migrations run automatically on `serve` and non-dry `import`, inside a transaction protected by an advisory lock. The database role needs migration privileges, including permission to enable PostGIS if it has not already been enabled. Production operators can provision the extension beforehand. `search`, `remove`, `inspect`, and dry run never run migrations.

Back up both the database and your imagery separately. The catalog stores local
asset paths, so preserve the mount layout when restoring or moving a deployment.
See the [database schema](development.md#database-schema) for constraints and indexes.

## Web access and read-only viewing

Each `aarde serve` startup generates a fresh random 256-bit bearer token. The startup log reports `token_file` and `public_read`, never the token itself. The token is atomically written with mode `0600` inside a private directory (`0700`), rotated on restart, and removed on clean shutdown. Aarde creates missing token directories and rejects an existing directory that grants group or other access. Use a separate token path for each server instance.

The native default is `$XDG_RUNTIME_DIR/aarde/web.token`, falling back to `$XDG_STATE_HOME/aarde/web.token`, then `$HOME/.local/state/aarde/web.token`. `AARDE_WEB_TOKEN_PATH` overrides it. Docker uses `/home/aarde/.local/state/aarde/web.token`; read it with:

```sh
docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token
```

Paste the token into the login screen. It is sent as an `Authorization: Bearer …` header and remembered in this browser tab's session storage. **Sign out** clears it and removes the catalog view. Restarting the server invalidates previous tokens; the next rejected request clears the browser session and asks for the current token.

To enable public viewing, set `AARDE_WEB_PUBLIC_READ=true` in Compose's `.env` and run `docker compose up -d aarde`, or export it before running `aarde serve`. Unset or empty means false; boolean values such as `true`/`false` and `1`/`0` are accepted. Invalid values prevent startup.

Public viewers see **Read only** and can browse catalogs, metadata, local asset paths, and footprints, search exact IDs, draw/edit search areas, and run polygon searches. `POST /api/v1/imagery/search` is explicitly allowed because it only reads the catalog. Viewer manifests, rendered images, and geometry always require a token, even with public reads enabled. Unknown API routes and other methods still require a token. An explicitly supplied invalid token is always rejected, even with public reads enabled.

**Sign in** accepts the current token; **Lock** clears it and returns to public viewing. An expired or rejected token also returns to public viewing when enabled. Aarde's entire web API is currently read-only, including authenticated sessions. Imports remain local CLI operations with filesystem/database access; signing in does not add web editing or import controls. API responses use `Cache-Control: no-store`, while the login page and frontend assets remain accessible without a token.

## Upgrading an existing Ruimte catalog

Use `aarde` instead of `ruimte`, and rename `RUIMTE_*` environment variables to `AARDE_*`. The database URL can keep its existing database name and role. On startup, Aarde automatically renames the old migration-history table and preserves the imagery records.

New Compose installations use the `aarde` project name. To reuse an existing Compose catalog, keep its original project name (which determines the volume name) and PostgreSQL credentials. For an installation created with the original defaults, set `POSTGRES_DB=ruimte`, `POSTGRES_USER=ruimte`, and the original `POSTGRES_PASSWORD` in `.env`, then run:

```sh
docker compose -p ruimte up -d --build --remove-orphans
```

Replace `ruimte` in that command with the original Compose project name if it differed. This reuses the existing catalog volume; PostgreSQL does not rename databases or roles when its initialization environment changes. Do not remove that volume during the upgrade.
