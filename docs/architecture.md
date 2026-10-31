# Architecture — URL Shortener

**Status:** MVP baseline  
**Decision scope:** Issue #1  
**Runtime:** Go 1.23  
**Architecture style:** Modular monolith

## 1. Product boundary

The service accepts a destination URL, assigns it a short code, stores the mapping, and redirects public requests for that code to the destination.

An authenticated account owns the URLs it creates. The owner can list links, inspect metadata and click statistics, and deactivate a link. Visitors do not need an account to follow a short link.

### MVP capabilities

- Account registration and login.
- Create a short URL with an optional custom alias and expiry.
- Redirect active, unexpired codes.
- List and inspect links owned by the authenticated user.
- Deactivate an owned link.
- Show aggregate click counts by UTC day.
- Health/readiness endpoints, structured logs, tests, and CI.

### Explicit non-goals

Custom domains, teams, QR codes, bulk import, campaign management, geo/device analytics, multi-region replication, Kafka, Kubernetes, and microservices are not part of the MVP. Revisit them only when a real requirement or measured bottleneck justifies the added complexity.

## 2. System context

~~~text
                         ┌──────────────────────────────┐
Browser / API client ───▶│ Go HTTP service              │
                         │                              │
                         │  Auth + URL + Redirect       │
                         │  + Analytics handlers        │
                         └──────────┬─────────┬─────────┘
                                    │         │
                        durable data│         │hot lookup / limits
                                    ▼         ▼
                              ┌──────────┐ ┌──────────┐
                              │PostgreSQL│ │  Redis   │
                              │ source   │ │ cache +  │
                              │ of truth │ │ limits   │
                              └──────────┘ └──────────┘
                                    ▲
                                    │ batched aggregates
                              ┌──────────────┐
                              │ Analytics    │
                              │ worker        │
                              └──────────────┘
~~~

There is one deployable Go service. Internally, modules have explicit responsibilities and communicate through service-level interfaces. PostgreSQL owns durable state. Redis is disposable acceleration and shared coordination state; losing Redis must not lose URL mappings.

## 3. Technology decisions

| Concern | MVP decision | Reason |
|---|---|---|
| Language | Go 1.23 | Matches the current module baseline |
| HTTP | Standard net/http | Small dependency surface; method-aware routing is sufficient |
| Architecture | Modular monolith | Simple to build and deploy while retaining clear boundaries |
| Primary database | PostgreSQL | Durable mappings, constraints, ownership, and transactions |
| Cache | Redis | Hot redirect lookups and distributed rate-limit counters |
| IDs | UUID primary keys | Stable internal identifiers without exposing database sequence values |
| Short codes | 10 random Base62 characters | About 59.5 bits of code space; generated independently of row IDs |
| Password hashing | Argon2id | Memory-hard password hashing |
| Authentication | Short-lived signed bearer access token | Simple API authentication for the first release |
| Migrations | Versioned SQL migrations | Auditable, repeatable schema changes |
| Local dependencies | Docker Compose | Reproducible developer environment |
| CI | GitHub Actions | Formatting, tests, static checks, and builds |

Do not introduce a message broker, separate services, or a distributed ID generator for the MVP.

## 4. Module boundaries

Suggested layout:

~~~text
cmd/
  api/
    main.go
internal/
  config/
  http/
    handlers/
    middleware/
    response/
  auth/
  user/
  url/
  analytics/
  storage/
    postgres/
  cache/
    redis/
migrations/
docs/
deploy/
~~~

Responsibilities:

- **config:** parse and validate environment configuration. Do not let packages read process environment directly.
- **http:** route registration, request decoding, middleware, response formatting, and HTTP status mapping. Handlers remain thin.
- **auth:** password hashing, token creation/validation, and authenticated principal context.
- **user:** account use cases and user repository contract.
- **url:** URL validation, code generation, ownership rules, expiry, and create/read/deactivate use cases.
- **analytics:** click recording interface, bounded background worker, aggregation, and stats queries.
- **storage/postgres:** concrete SQL repositories and transaction handling.
- **cache/redis:** cache and rate-limit adapters. Redis details must not leak into URL business rules.

Dependency direction:

~~~text
HTTP handlers ──▶ application services ──▶ repository/cache interfaces
                                           ▲              ▲
                                           │              │
                                      PostgreSQL        Redis
~~~

Business services define only the interfaces they need. Avoid interfaces that simply duplicate every method of a concrete type without providing a useful boundary.

## 5. Core data model

The first schema should contain:

### users

| Column | Type | Rule |
|---|---|---|
| id | UUID | Primary key |
| email | text | Not null, unique; normalized before storage |
| password_hash | text | Not null; Argon2id encoded hash |
| created_at | timestamptz | Not null, UTC |
| updated_at | timestamptz | Not null, UTC |

### urls

| Column | Type | Rule |
|---|---|---|
| id | UUID | Primary key |
| user_id | UUID | Not null, FK to users |
| short_code | varchar(32) | Not null, unique |
| original_url | text | Not null |
| created_at | timestamptz | Not null, UTC |
| expires_at | timestamptz | Nullable; must be later than creation |
| is_active | boolean | Not null, default true |

Required indexes: unique index on short_code and a user_id + created_at index for owner listing. Add further indexes only for queries that are actually used.

The analytics milestone will add a daily aggregate table keyed by (url_id, date_utc). It should store counts only; do not collect raw IP addresses or full user-agent strings in the MVP.

## 6. Short-code generation

- Generate 10 characters from the alphabet A–Z, a–z, and 0–9 using crypto/rand.
- Use rejection sampling to avoid modulo bias. For one random byte, accept values below 248 (62 × 4), then map the accepted value modulo 62.
- Keep the generated code case-sensitive.
- Enforce uniqueness with a PostgreSQL unique constraint; an application-side pre-check is not sufficient.
- On a unique violation, generate another code and retry a bounded number of times (maximum 5 attempts). Return a controlled internal error if all attempts collide.
- Custom aliases use the same unique namespace and therefore conflict with both existing aliases and generated codes.

Do not derive short codes from sequential database IDs or timestamps; they are guessable and couple the public identifier to internal storage.

## 7. URL validation and alias rules

Destination URL validation:

- Accept only absolute HTTP or HTTPS URLs.
- Require a non-empty hostname.
- Reject malformed URLs, control characters, and URL user-info credentials.
- Limit the submitted URL to 2,048 bytes.
- Preserve the submitted path, query, and fragment; do not silently rewrite destination semantics.
- The service redirects to the destination; it does not fetch the destination server-side.

Custom alias validation:

- Optional; generated codes are used when it is absent.
- Length: 4–32 characters.
- Allowed characters: letters, digits, hyphen, and underscore.
- Case-sensitive.
- Reserved aliases: api, healthz, readyz, favicon.ico, and robots.txt.
- Reject aliases that conflict with any existing short code.

## 8. Expiration and deactivation

- Links do not expire by default.
- An optional expires_at must be a valid RFC 3339 timestamp strictly in the future at creation time.
- Store timestamps as timestamptz and compare using UTC server/database time.
- An expired link returns HTTP 410 Gone.
- Unknown and inactive links return HTTP 404 Not Found.
- Deactivation is a soft state change (is_active = false); do not physically delete the mapping in the MVP.

This keeps link lifecycle behavior explicit and gives the owner an irreversible deactivate operation without exposing another user's record.

## 9. Request/data flows

### Create a short URL

1. Client sends POST /api/v1/urls with a bearer access token.
2. Authentication middleware validates the token and attaches the user ID to request context.
3. Handler decodes and validates the JSON body.
4. URL service validates the destination, optional alias, and expiration.
5. Code generator creates a random code unless a custom alias was supplied.
6. Repository inserts the mapping with the authenticated user ID. The database unique constraint is the final collision guard.
7. Handler returns 201 Created and builds short_url from the configured PUBLIC_BASE_URL.

Never accept user_id from the request body as an ownership authority.

### Resolve a short link

1. Visitor requests GET /{code}; no authentication is required.
2. Redirect handler validates the code format.
3. Service checks Redis for a cached mapping.
4. On cache miss, service queries PostgreSQL for the code.
5. Inactive or missing rows return 404; expired rows return 410.
6. On an active mapping, cache the destination with a bounded TTL and its absolute expiry.
7. Enqueue a click record for analytics without waiting for aggregation to finish.
8. Return HTTP 302 with Location set to the original URL.

A Redis failure is treated as a cache miss. PostgreSQL remains authoritative. A cached record must still be checked against its absolute expires_at before redirecting.

### Deactivate a link

1. Authenticate the caller.
2. Update the row only where both short_code and user_id match.
3. If no owned row matches, return 404 (do not reveal another user's resource).
4. After the database change commits, delete the corresponding Redis key.
5. Record/measure cache invalidation failures; the redirect cache TTL is bounded to limit stale entries.

### Record analytics

The redirect handler submits a small event to a bounded in-process queue. A worker batches events and atomically increments UTC daily aggregates in PostgreSQL. If the queue is full, the event is dropped and a metric is incremented; a process crash can lose events still in memory. Analytics are therefore best-effort in the MVP and must never cause a successful redirect to fail. A durable queue can be considered later if requirements demand it.

## 10. Redis cache policy

- Key format: short-url:v1:{code}.
- Cache only active mappings.
- Maximum cache TTL: 5 minutes; if the link expires sooner, TTL must not exceed the remaining lifetime.
- Store expires_at with the cached value and verify it at read time.
- Do not cache unknown, inactive, or expired codes in the MVP.
- On deactivation, delete the cache key after committing the database update.
- Cache read/write/delete errors are observable but must not turn a healthy PostgreSQL redirect into a 5xx response.

Redis can be unavailable without making URL creation or redirects impossible, provided PostgreSQL is healthy. Readiness should require PostgreSQL; Redis availability is a degraded-mode signal, not a hard readiness dependency.

## 11. Authentication and authorization

- Registration and login are public endpoints.
- Normalize email (trim and lowercase) before lookup/storage.
- Hash passwords with Argon2id; never store or log plaintext passwords.
- Issue signed bearer access tokens with a 15-minute lifetime. Validate signature, expiry, issuer, audience, and subject. Pin the accepted signing algorithm in configuration/code; never accept an algorithm selected by an untrusted token.
- Keep signing material outside source control and support rotation through deployment configuration.
- Refresh tokens, password reset, email verification, and social login are out of scope for the first release.
- URL management and statistics endpoints require authentication.
- Ownership is always enforced in the database query using the authenticated user ID.
- For a resource owned by someone else, return 404 rather than 403 to avoid confirming its existence.
- Public redirects do not require authentication.

## 12. HTTP behavior and errors

API errors use one response shape:

~~~json
{
  "error": {
    "code": "invalid_url",
    "message": "url must be an absolute HTTP or HTTPS URL",
    "request_id": "request-correlation-id"
  }
}
~~~

Do not expose SQL, stack traces, token contents, or internal configuration in error responses.

Common status mapping:

| Status | Meaning |
|---|---|
| 200 | Successful read or login |
| 201 | Resource created |
| 204 | Deactivation succeeded |
| 302 | Public redirect |
| 400 | Invalid JSON or validation failure |
| 401 | Missing or invalid authentication |
| 404 | Unknown, inactive, or not-owned resource |
| 409 | Email or custom alias already exists |
| 410 | Link has expired |
| 429 | Rate limit exceeded (introduced in hardening milestone) |
| 500 | Unexpected internal failure |
| 503 | Required dependency unavailable for the requested operation |

## 13. Runtime and operational behavior

- Configure the public origin through PUBLIC_BASE_URL; do not build short links from an untrusted Host header.
- Configure server read-header, read, write, and idle timeouts.
- Handle SIGINT/SIGTERM with graceful shutdown and close database/cache connections.
- /healthz reports process liveness only.
- /readyz reports whether the service can serve core requests; PostgreSQL is required. Redis degradation is reported separately.
- Use structured logs with request IDs, route, status, and duration.
- Never log passwords, bearer tokens, or full sensitive request bodies.
- Expose operational metrics for request latency/status, database and Redis errors, cache hit/miss, generated-code collision retries, and dropped analytics events.

## 14. Architecture decisions to revisit with evidence

| Topic | MVP position | Revisit when |
|---|---|---|
| Redirect cache | Redis cache-aside, max 5-minute TTL | Traffic or latency measurements require tuning |
| Analytics durability | Best-effort bounded worker queue | Product requires exact counts or audit-grade events |
| Single service | Modular monolith | Independent deployment/scaling is demonstrably needed |
| One PostgreSQL primary | Source of truth | Measured read/write capacity or availability requires replicas/partitioning |
| 302 redirects | Default response | Product semantics explicitly require a different cache behavior |

The implementation should favor correctness and observable behavior over premature infrastructure complexity.
