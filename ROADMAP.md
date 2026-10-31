# URL Shortener — Build Roadmap

A production-minded URL shortener built as a **Go modular monolith**. PostgreSQL is the source of truth; Redis accelerates redirects and supports rate limiting.

## Product scope

Users can create short links, optionally choose an alias or expiration, share the generated link, and inspect click statistics. Public visitors can resolve short links without signing in. Authenticated users can manage only links they own.

## Architecture at a glance

```text
                    ┌─────────────────────┐
Client ────────────▶│ Go HTTP API          │
                    │ /api/v1/* and /{code}│
                    └──────┬────────┬─────┘
                           │        │
                    writes/reads    │ hot redirect lookup
                           ▼        ▼
                    ┌──────────┐  ┌──────────┐
                    │PostgreSQL│  │  Redis   │
                    │ source   │  │ cache +  │
                    │ of truth │  │ limits   │
                    └──────────┘  └──────────┘
                           ▲
                           │ click aggregates/events
                    ┌──────────────┐
                    │ Analytics    │
                    │ module       │
                    └──────────────┘
```

Keep the MVP as one deployable service with clear internal boundaries. Do not add Kafka, Kubernetes, microservices, or a distributed ID generator until measured requirements justify them.

## Detailed design documents

- [Architecture](./docs/architecture.md) — system boundaries, modules, data model, request flows, security, cache behavior, and operational decisions.
- [HTTP API contract](./docs/api.md) — endpoint behavior, JSON examples, validation, authentication, status codes, and error format.

## Technology choices

| Area | Choice | Reason |
|---|---|---|
| Language | Go 1.23 | Existing module baseline; simple deployment and concurrency |
| HTTP | `net/http` | Keep the first version small and explicit |
| Database | PostgreSQL | Durable source of truth, constraints, and relational ownership |
| Cache | Redis | Fast redirect lookups and shared rate-limit state |
| Migrations | Versioned SQL migrations | Repeatable schema changes |
| Local environment | Docker Compose | One-command local dependencies |
| CI | GitHub Actions | Formatting, tests, static checks, and builds |
| Deployment | Decide after MVP | Avoid choosing infrastructure before runtime needs are known |

## API sketch

| Method | Route | Purpose |
|---|---|---|
| POST | `/api/v1/urls` | Create a short URL |
| GET | `/api/v1/urls` | List the authenticated user's links |
| GET | `/api/v1/urls/{code}` | Read owned URL metadata |
| GET | `/api/v1/urls/{code}/stats` | Read click statistics |
| DELETE | `/api/v1/urls/{code}` | Deactivate an owned link |
| GET | `/{code}` | Public redirect (302) |
| GET | `/healthz` | Process liveness |
| GET | `/readyz` | Dependency readiness |

Use a consistent JSON error shape for API routes. Redirects are public; management and statistics routes require authentication. Validate destination URLs and allow only HTTP/HTTPS.

## Delivery milestones

### Milestone 1 — Foundation
- [x] Agree on system boundaries and API contract — [#1 Architecture and API contract](https://github.com/nitinvedi/url-shortener/issues/1)
- [x] Bootstrap server, configuration, logging, health checks, and Docker Compose — [#2 Service foundation](https://github.com/nitinvedi/url-shortener/issues/2)
- [x] Create PostgreSQL schema, migrations, and repository layer — [#3 Persistence](https://github.com/nitinvedi/url-shortener/issues/3)

**Exit criteria:** the service starts locally, dependencies are reproducible, and database migrations run from an empty database.

### Milestone 2 — Identity and core shortening
- [x] Implement registration, login, token validation, and authentication middleware (Phase A) — [#6 Authentication and ownership](https://github.com/nitinvedi/url-shortener/issues/6). Owner-scoped management remains for Milestone 3.
- [x] Create URLs, validate destinations, and generate random Base62 codes — [#4 URL creation](https://github.com/nitinvedi/url-shortener/issues/4)
- [x] Resolve codes and redirect using Redis cache-aside — [#5 Redirect path](https://github.com/nitinvedi/url-shortener/issues/5)

**Exit criteria:** users can authenticate; an authenticated user can create a short URL; a public visitor can resolve it; unknown, expired, and inactive codes behave consistently; Redis failure does not take down redirects when PostgreSQL is healthy.

### Milestone 3 — Ownership and product features
- [ ] Complete owner-scoped URL listing, metadata, and deactivation as part of [#6 Authentication and ownership](https://github.com/nitinvedi/url-shortener/issues/6)
- [ ] Add click aggregation and URL statistics — [#7 Analytics](https://github.com/nitinvedi/url-shortener/issues/7)

**Exit criteria:** users can manage only their own links; statistics are available only to the owner; public redirects stay unauthenticated.

### Milestone 4 — Production readiness
- [ ] Add rate limits, automated test layers, metrics, and structured logs — [#8 Hardening and observability](https://github.com/nitinvedi/url-shortener/issues/8)
- [ ] Add CI, production container build, deployment runbook, and release process — [#9 Delivery and operations](https://github.com/nitinvedi/url-shortener/issues/9)

**Exit criteria:** every pull request runs automated checks; the service has documented deployment, health checks, configuration, and rollback/forward-fix procedures.

## Suggested repository structure

```text
cmd/
  api/
    main.go
internal/
  config/
  http/
  url/
  user/
  storage/
    postgres/
  cache/
    redis/
  analytics/
migrations/
deploy/
docs/
  architecture.md
  api.md
  runbook.md
```

Keep handlers thin. Put business rules in services, persistence behind repository interfaces, and Redis behind a cache interface. Avoid interfaces that merely mirror every concrete method without a real boundary.

## Engineering rules

1. Build in small branches and pull requests; one issue should generally map to one focused PR.
2. Add tests with each behavior, not as a final cleanup phase.
3. PostgreSQL constraints are the final guard for uniqueness and ownership relationships.
4. Redis is disposable acceleration, never the only copy of a URL.
5. Never trust a client-provided user ID; derive identity from verified authentication.
6. Do not log passwords, access tokens, or unnecessary personal data.
7. Measure performance before introducing extra infrastructure or claiming throughput.

## Deliberately deferred

Custom domains, QR codes, team accounts, bulk imports, geo/device analytics, Kafka/event streaming, Kubernetes, and multi-region replication. Revisit these only when there is a concrete product or scale requirement.

## Working order

Start with **#1 → #2 → #3 → authentication in #6 → #4 → #5**. Complete the remaining ownership work in #6 after URL persistence/use cases exist. Do not begin analytics or scale optimization before the create-and-redirect flow is reliable.
