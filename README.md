# Lyrebird

> A lyrebird mimics any sound it hears. **Lyrebird** mimics any HTTP(S) API — an agent-first,
> spy-by-default mock & proxy server, driven primarily over **MCP**, deployed as a single Docker
> image.

By default Lyrebird records every call it sees and transparently proxies it through to the real
upstream (spy mode). The moment a mock rule matches a call, Lyrebird returns the mock instead — no
mode switch, no restart. Everything (creating mocks, scripting responses, reading traffic/metrics,
promoting a recorded call into a permanent mock) happens over MCP, with a thin Admin REST twin for
curl/scripts/health checks. There is no UI.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design and [`specs/001-lyrebird/`](specs/001-lyrebird/)
for the spec, contracts, and quickstart walkthrough this README summarizes.

## Run it

Pull and run the published image:

```bash
docker run -p 8080:8080 -p 9090:9090 ghcr.io/brienze1/lyrebird:latest
```

- Port `8080` — the **data plane**: point whatever you're testing at this (mocked/spied traffic).
- Port `9090` — the **control plane**: MCP (`/mcp`) and Admin REST (`/__lyrebird/...`).

Or via `docker-compose.yml` (mounts `./config` for seed mocks and a named volume for the SQLite
store):

```bash
docker compose up
```

Or run from source (every setting has a default — nothing is required to start):

```bash
go run ./cmd/lyrebird
```

## Connect an MCP client

Lyrebird's MCP server is reachable over Streamable HTTP at `/mcp` on the control-plane port:

```
http://localhost:9090/mcp
```

Point any MCP-capable agent/client at that URL. A minimal manual check with `curl`:

```bash
curl -s http://localhost:9090/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

For a local, single-user session without any HTTP listeners at all, run Lyrebird in **stdio mode**
instead (see `LYREBIRD_MCP_STDIO` below) and point a stdio-based MCP client at the process directly.

Once connected, start with the `lyrebird_guide` tool — it documents every other tool with a minimal
working example.

## Seeding mocks at boot

Mount a directory of YAML files at `/config` (or wherever `LYREBIRD_SEED_DIR` points) — each file
declares a space, its upstreams, and its mocks. These load once at boot as **seeded** mocks:
always-up, immune to `reset`/GC/TTL. See `specs/001-lyrebird/contracts/seed-config.md` for the exact
schema, and `GET /__lyrebird/export` / MCP's `export_config` to generate one from a space's current
runtime state.

## Environment variables

Every variable is optional; Lyrebird runs with sane defaults if none are set.

| Variable | Purpose | Default |
|---|---|---|
| `LYREBIRD_DATA_PORT` | data-plane listen port | `8080` |
| `LYREBIRD_CONTROL_PORT` | control-plane listen port (MCP + Admin REST) | `9090` |
| `LYREBIRD_DEFAULT_SPACE` | default partition/space name | `default` |
| `LYREBIRD_SEED_DIR` | directory of seed config YAML, loaded at boot | `/config` |
| `LYREBIRD_DB_PATH` | SQLite store file path (traffic log + ephemeral mocks) | `/data/lyrebird.db` |
| `LYREBIRD_DATA_KEY` | base64, 32-byte at-rest encryption key | unset — random key generated each boot |
| `LYREBIRD_TRAFFIC_TTL` | how long recorded traffic is kept before GC prunes it | `24h` |
| `LYREBIRD_GC_INTERVAL` | how often the GC sweep runs | `1m` |
| `LYREBIRD_UPSTREAM_TIMEOUT` | timeout for a proxied call to the real upstream | `10s` |
| `LYREBIRD_SCRIPT_TIMEOUT` | execution cap for a mock's match/respond JS script | `100ms` |
| `LYREBIRD_BODY_CAP_BYTES` | max request/response body size recorded to the traffic log | `1048576` (1 MiB) |
| `LYREBIRD_ALLOW_PROXY_HOSTS` | comma-separated allow-list of hosts the proxy may forward to | unset — every host allowed |
| `LYREBIRD_AUTH_KEYS` | comma-separated client keys; **presence enables control-plane auth** | unset — control plane open |
| `LYREBIRD_TOKEN_TTL` | TTL of a JWT issued by `POST /__lyrebird/auth/token` (only relevant once auth is enabled) | `1h` |
| `LYREBIRD_MITM_ENABLED` | enable transparent forward-proxy/MITM mode | unset — disabled |
| `LYREBIRD_MITM_CA_CERT_FILE` / `LYREBIRD_MITM_CA_KEY_FILE` | mount a stable CA (cert+key pair) for MITM instead of a fresh one each boot | unset — CA regenerated each boot |
| `LYREBIRD_MCP_STDIO` | run MCP over stdio only, with no HTTP listeners at all | unset — HTTP daemon mode |

Notes:
- **The data plane is never authenticated** — the system under test can't carry a Lyrebird token, so
  auth only ever gates the control plane (MCP + Admin REST).
- **Encryption at rest is always on.** Request/response bodies, mock scripts, and mock response
  bodies are AES-256-GCM encrypted before being written to SQLite; only routing metadata (space,
  method, path, host, status, timestamps) stays in clear for filtering/GC. Set `LYREBIRD_DATA_KEY` to
  make a mounted `/data` volume decryptable across restarts — omit it and a restart is a clean slate,
  which is the right default for disposable state.

## Learn more

- [`docs/DESIGN.md`](docs/DESIGN.md) — architecture, domain model, and the reasoning behind the
  major decisions (spy-by-default, disposable state, MCP-primary).
- [`specs/001-lyrebird/spec.md`](specs/001-lyrebird/spec.md) — the full functional spec.
- [`specs/001-lyrebird/quickstart.md`](specs/001-lyrebird/quickstart.md) — a scenario-by-scenario
  walkthrough (spy, mocks, MCP workflow, scripting, multi-tenancy, GC, auth, delivery).
- [`specs/001-lyrebird/contracts/`](specs/001-lyrebird/contracts/) — the MCP tool contract, Admin
  REST contract, and seed-config YAML schema.

## License

[MIT](LICENSE) — free to use, modify, and redistribute, including commercially. The published
Docker image (`ghcr.io/brienze1/lyrebird`) carries the same license (see its `LICENSE` file and
`org.opencontainers.image.licenses` label).
