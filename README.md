# URL Shortener

A production-minded URL shortener built with Go, PostgreSQL, and Redis.

The project is being developed incrementally, starting with a reliable shortening and redirect flow, then adding authentication, ownership, analytics, and production operations.

## Project plan

- [Roadmap](./ROADMAP.md) — milestones and implementation issues.
- [Architecture](./docs/architecture.md) — system boundaries, data model, request flows, caching, and security decisions.
- [HTTP API contract](./docs/api.md) — endpoint behavior, JSON payloads, validation, and status codes.

## Technology stack

- **Go 1.23** — HTTP service
- **PostgreSQL** — durable source of truth
- **Redis** — redirect cache and rate-limit state
- **Docker Compose** — local dependencies
- **GitHub Actions** — formatting, PostgreSQL integration tests, race checks, static analysis, and build

## Local development

### Prerequisites

- Go 1.23 or newer
- Docker Engine with the Docker Compose plugin (for PostgreSQL and Redis)

### Run the API without Docker

```bash
go run ./cmd/api
```

The API requires PostgreSQL. Set `DATABASE_URL` to a PostgreSQL connection URL (a local default is configured for development), then start PostgreSQL before running the server. It also requires `JWT_SIGNING_KEY` to be at least 32 bytes; the checked-in `.env.example` contains a local-only development key. The server listens on `:8080` by default, applies embedded SQL migrations at startup, and checks PostgreSQL readiness. Check it with:

```bash
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

### Run the local stack

```bash
cp .env.example .env
docker compose up --build
```

The API is available at `http://localhost:8080`. PostgreSQL and Redis are exposed on ports 5432 and 6379 by default. The API connects to PostgreSQL and applies migrations automatically at startup. Redis is started for the upcoming caching milestone but is not connected yet. PostgreSQL data is stored in the named `postgres_data` volume. Register with `POST /api/v1/auth/register`, then log in with `POST /api/v1/auth/login`; both return a 15-minute Bearer access token.

Stop the stack with:

```bash
docker compose down
```

Stop the stack and delete local PostgreSQL data with `docker compose down -v`.

### Configuration

All settings are environment variables. Defaults are suitable for local development, not production.

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP listen address in host:port form |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Maximum time to read request headers |
| `HTTP_READ_TIMEOUT` | `10s` | Maximum time to read the full request |
| `HTTP_WRITE_TIMEOUT` | `15s` | Maximum time to write a response |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `HTTP_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `DATABASE_URL` | local PostgreSQL URL | PostgreSQL connection URL |
| `DB_MAX_OPEN_CONNS` | `25` | Maximum open database connections |
| `DB_MAX_IDLE_CONNS` | `5` | Maximum idle database connections |
| `DB_CONN_MAX_LIFETIME` | `30m` | Maximum lifetime of a pooled connection |
| `DB_CONN_MAX_IDLE_TIME` | `5m` | Maximum idle time for a pooled connection |
| `DB_CONNECT_TIMEOUT` | `5s` | Startup database ping timeout |
| `JWT_SIGNING_KEY` | local-only development key | HS256 signing key, minimum 32 bytes; use a secret manager in production |
| `JWT_ISSUER` | `url-shortener` | Required token issuer claim |
| `JWT_AUDIENCE` | `url-shortener-api` | Required token audience claim |

Invalid addresses, database URLs, pool sizes, non-positive durations, short JWT signing keys, and unknown log levels fail fast at startup. The local JWT key must be replaced with a unique secret in every deployed environment. Database migrations are forward-only and run automatically on startup; see [migration rules](./internal/storage/postgres/migrations/README.md).

Copy `.env.example` to `.env` to configure local Compose ports and PostgreSQL credentials. `.env` is ignored by Git. Never commit real credentials.

### Useful commands

```bash
make run
make test
make test-race
make test-integration TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/shortener_test?sslmode=disable"
make vet
make build
make compose-up
make compose-down
```

## Current status

- [x] Architecture and API design documented.
- [x] Go HTTP foundation, environment configuration, health endpoints, and local Compose services.
- [x] PostgreSQL schema, forward-only migrations, connection pool, and repositories implemented.
- [x] PostgreSQL integration tests verified in GitHub Actions against PostgreSQL 16.
- [x] Authentication Phase A: registration, login, Argon2id password hashing, signed 15-minute access tokens, and verified-token middleware.
- [ ] URL creation and public redirect flows; see [issue #4](https://github.com/nitinvedi/url-shortener/issues/4).
- [ ] Analytics, hardening, CI, and deployment.

See [ROADMAP.md](./ROADMAP.md) for the full sequence.
