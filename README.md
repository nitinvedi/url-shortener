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
- **GitHub Actions** — planned continuous integration

## Local development

### Prerequisites

- Go 1.23 or newer
- Docker Engine with the Docker Compose plugin (for PostgreSQL and Redis)

### Run the API without Docker

```bash
go run ./cmd/api
```

The server listens on `:8080` by default. Check it with:

```bash
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

### Run the local stack

```bash
cp .env.example .env
docker compose up --build
```

The API is available at `http://localhost:8080`. PostgreSQL and Redis are exposed on ports 5432 and 6379 by default. At this foundation stage the API does not yet connect to either dependency; database and cache integration are tracked in later issues.

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

Invalid addresses, non-positive durations, and unknown log levels fail fast at startup.

Copy `.env.example` to `.env` to configure local Compose ports and PostgreSQL credentials. `.env` is ignored by Git. Never commit real credentials.

### Useful commands

```bash
make run
make test
make test-race
make vet
make build
make compose-up
make compose-down
```

## Current status

- [x] Architecture and API design documented.
- [x] Go HTTP foundation, environment configuration, health endpoints, and local Compose services.
- [ ] PostgreSQL schema, migrations, and repository layer.
- [ ] Authentication and URL creation/redirect flows.
- [ ] Analytics, hardening, CI, and deployment.

See [ROADMAP.md](./ROADMAP.md) for the full sequence.
