# GoChat

A self-hosted team chat in the spirit of Mattermost: teams, public and private
channels, direct messages, one-time invite links, attachments, presence and
typing indicators, all delivered in real time. The whole application runs on
the [hollywood](https://github.com/anthdm/hollywood) actor engine, on both the
server and the web gateway, and the browser UI is server-rendered HTML driven
by [htmx](https://htmx.org) over a WebSocket.

## Architecture

```
 browser ──HTTPS/WS──▶ gochat web ──DRPC (protobuf)──▶ gochat server ──▶ ScyllaDB
  (htmx)               gateway actors                    service actors    MinIO
```

**`gochat server`** owns all state and rules.

| Actor | Role |
| --- | --- |
| `accounts` (pool) | sign-up, sign-in, argon2id hashing, sessions, TOTP, recovery codes |
| `directory` (pool) | teams, invites, channel listings, direct messages, profiles |
| `channels` → one actor per channel | membership, authorization, posting, edits, history, read state, typing |
| `files` (pool) | upload grants, downloads, orphan cleanup |
| `hub` | live connections of every web gateway, event fan-out, presence |

A channel actor is the single writer for its channel, so ordering and
membership checks never race. It loads lazily on first use and passivates when
idle. Blocking work (password hashing, Scylla reads) runs in worker pools so it
never stalls a mailbox.

**`gochat web`** is a stateless gateway. Each page gets one WebSocket served by
a session actor; a router actor multiplexes every session over a single
bidirectional DRPC stream to the server and reattaches them after a reconnect.
Events arrive as protobuf and leave as htmx out-of-band HTML fragments. You
can run as many gateways as you like behind a load balancer.

The server is designed as a single node: channel actors and the hub live in
one process. Scylla and MinIO scale independently.

### Storage

- **ScyllaDB** holds everything. Messages are partitioned by
  `(channel_id, 10-day bucket)` with time UUIDs, so history pages read one
  partition at a time. Uniqueness (usernames, emails, team and channel names),
  invite consumption, the first-admin bootstrap and TOTP replay protection use
  lightweight transactions.
- **MinIO** stores attachments under `attachments/<channel>/<id>`. Uploads
  stream through the server, which sniffs the content type and enforces the
  size limit; attachments never posted are deleted by a janitor.
- **Casbin** policies live in Scylla too (custom adapter). Policies grant
  actions to role names; who holds which role in a team or channel is part of
  the membership rows.

## Security

- Passwords: argon2id with configurable cost; constant-time paths for unknown
  users; per-account and per-IP sign-in limits.
- Sessions: 256-bit random tokens, only their SHA-256 is stored; revocation
  (logout, password change, TOTP change) closes live sockets at once.
- TOTP: optional per user, secrets sealed with AES-256-GCM bound to the user
  id, codes single-use, ten hashed single-use recovery codes.
- Invites: one-time, expiring links; only the token hash is stored and it is
  consumed with a lightweight transaction.
- Web: strict CSP (no inline script, no eval), CSRF double-submit token plus
  `Origin` and `Sec-Fetch-Site` checks, `__Host-` `Secure` cookies and HSTS on
  https, `X-Frame-Options: DENY`, `nosniff`, per-IP rate limits.
- Content: messages are rendered as Markdown with raw HTML disabled; images
  become links so a message cannot make readers fetch outside URLs; links open
  with `rel="nofollow noopener noreferrer"`. Attachments are served with
  `Content-Disposition: attachment` unless they are safe images.
- Server API: every DRPC call carries the shared gateway key (checked in
  constant time) and the user's session; errors returned to gateways never
  carry internal causes. TLS and client certificates are supported on the DRPC
  link and on the Scylla connection.

## Requirements

- Go 1.27, [just](https://github.com/casey/just), [buf](https://buf.build)
- Docker with Compose v2
- golangci-lint (for `just lint`)

## Getting started

```bash
cp .env.template .env
```

Fill in `.env`: database and MinIO credentials, a `GATEWAY_KEY` of at least 32
characters and a `SERVICES_ACCOUNTS_SECRET_KEY` from `openssl rand -base64 32`.
If Docker Hub is not reachable, set `DOCKER_REGISTRY` to a mirror such as
`mirror.gcr.io/`.

### Everything in Docker

```bash
just up
```

This builds the image, starts Scylla and MinIO on an internal network, applies
the migrations and starts the server and the web gateway on
`http://localhost:8080` (`WEB_PORT`). The first account to sign up becomes the
system admin and is asked to create the first team; everyone else joins through
invite links (or freely when `services.accounts.open_signup` is on). Set
`SERVICES_ACCOUNTS_SETUP_TOKEN` to require a token for that first account when
the server is reachable by others before you set it up.

### Running the binaries on the host

```bash
just stores    # scylla on 127.0.0.1:9042, minio on 127.0.0.1:19000
just migrate
just server    # in one terminal
just web       # in another, then open http://localhost:8080
```

These use the development configs in [config/](config).

### Recipes

| Recipe | What it does |
| --- | --- |
| `just build` | regenerate protobuf code and build `.bin/gochat` |
| `just up` / `down` / `downv` | run, stop, or stop and wipe the Docker stack |
| `just logs <service>` | follow a service's logs |
| `just migrate-status` | list applied and pending migrations |
| `just proto` | lint and regenerate the protobuf and DRPC code |
| `just lint`, `just test`, `just test-race` | checks |
| `just pre-commit` | generate, tidy, lint and test |

## Command line

```
gochat server   [-p config-path] [-n config-name] [-e env-prefix]
gochat web      [-p config-path] [-n config-name] [-e env-prefix]
gochat migrate scylla up     <dir>
gochat migrate scylla status <dir>
gochat version
```

## Configuration

Each command embeds its defaults ([server](cmd/serve/default.yaml),
[web](cmd/web/default.yaml), [migrate](cmd/migrate/default.yaml)) and overlays
`<config-path>/<config-name>.yaml` and then environment variables. A key's
variable is its path in upper case with dots as underscores, optionally behind
a prefix: `database.scylla.password` → `DATABASE_SCYLLA_PASSWORD`. Unknown
keys are rejected at start-up so typos do not pass silently.

Secrets that should come from the environment:

| Variable | Used by |
| --- | --- |
| `DATABASE_SCYLLA_USERNAME`, `DATABASE_SCYLLA_PASSWORD` | server, migrate |
| `STORAGE_MINIO_ACCESS_KEY`, `STORAGE_MINIO_SECRET_KEY` | server |
| `SERVER_DRPC_GATEWAY_KEY` / `CLIENT_DRPC_GATEWAY_KEY` | server / web (same value) |
| `SERVICES_ACCOUNTS_SECRET_KEY` | server |
| `SERVICES_ACCOUNTS_SETUP_TOKEN` (optional) | server |

Set `web.public_url` to the origin browsers use; an `https` origin turns on
`Secure` cookies and HSTS. Behind a reverse proxy, list its addresses in
`web.trusted_proxies` so rate limits see client addresses.

The server exposes `/healthz`, `/readyz` and Prometheus `/metrics` on its admin
listener (`server.admin.address`). Keep it and the DRPC listener on a private
network, or enable TLS with client certificates on the DRPC link.

## Tests

```bash
just test
```

The end-to-end suite in [test/e2e](test/e2e) drives a running stack through the
web gateway: invites, real-time delivery, typing, attachments, private channel
isolation, CSRF, TOTP with recovery codes and session revocation. It needs an
existing admin account and team:

```bash
GOCHAT_E2E_URL=http://localhost:8080 GOCHAT_E2E_ADMIN=admin GOCHAT_E2E_ADMIN_PASSWORD=... GOCHAT_E2E_TEAM=acme go test ./test/e2e -count=1
```

## Layout

```
api/protobuf/      protobuf sources and generated protobuf + DRPC code
cmd/               cobra commands and embedded default configs
config/            configs for host runs and for the Docker stack
deployments/       Dockerfile and compose files
internal/actors    ask, pools and middleware on top of hollywood
internal/app       wiring of the server and web processes
internal/authz     casbin model and enforcer
internal/core      ids, passwords, TOTP, sealing, validation, models, caches
internal/repos     Scylla and MinIO repositories
internal/server    DRPC handlers and the admin listener
internal/services  hub, accounts, directory, channels, files
internal/web       HTTP handlers, gateway actors, templates and static assets
migrations/scylla  CQL migrations
test/e2e           end-to-end tests
```
