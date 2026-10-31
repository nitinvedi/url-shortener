# HTTP API Contract

**Version:** v1  
**Base path:** /api/v1  
**Public redirect path:** /{code}  
**Authentication:** Bearer access token for account and URL-management routes

This document defines the MVP contract. JSON field names are snake_case. Timestamps use RFC 3339 and are stored in UTC.

## 1. Conventions

### Base URL

The public short-link origin is configured through PUBLIC_BASE_URL, for example:

~~~text
https://sho.rt
~~~

The API must not construct short URLs from an incoming Host header.

### Authentication

Protected routes require:

~~~http
Authorization: Bearer <access_token>
~~~

Access tokens expire after 15 minutes. The implementation pins the signing algorithm to HS256 and verifies the signature plus issuer, audience, subject, issued-at, and expiry claims. The signing key is provided through `JWT_SIGNING_KEY` (at least 32 bytes); use a managed secret in deployed environments. Missing, malformed, expired, or invalid tokens return 401.

### Common error format

~~~json
{
  "error": {
    "code": "invalid_url",
    "message": "url must be an absolute HTTP or HTTPS URL",
    "request_id": "req_..."
  }
}
~~~

The request_id correlates the response with structured server logs. Never return stack traces, SQL details, secrets, or token contents.

### Validation rules

- Destination URL must be an absolute HTTP or HTTPS URL with a hostname.
- Reject URL user-info credentials and malformed input.
- Maximum destination URL length: 2,048 bytes.
- A custom_alias is optional; when supplied it must be 4–32 characters from A–Z, a–z, 0–9, hyphen, or underscore.
- Custom aliases are case-sensitive.
- Reserved aliases: api, healthz, readyz, favicon.ico, robots.txt.
- expires_at is optional, RFC 3339, and strictly in the future when the link is created.
- Links have no expiry unless expires_at is supplied.

## 2. Endpoint summary

| Method | Path | Auth | Success |
|---|---|---|---|
| POST | /api/v1/auth/register | No | 201 |
| POST | /api/v1/auth/login | No | 200 |
| POST | /api/v1/urls | Yes | 201 |
| GET | /api/v1/urls | Yes | 200 |
| GET | /api/v1/urls/{code} | Yes | 200 |
| GET | /api/v1/urls/{code}/stats | Yes | 200 |
| DELETE | /api/v1/urls/{code} | Yes | 204 |
| GET | /{code} | No | 302 |
| GET | /healthz | No | 200 |
| GET | /readyz | No | 200 or 503 |

Register/login are included so the URL API has a fully specified identity contract even though the implementation is tracked in its own issue.

## 3. Register an account

~~~http
POST /api/v1/auth/register
Content-Type: application/json
~~~

Request:

~~~json
{
  "email": "dev@example.com",
  "password": "correct horse battery staple"
}
~~~

Rules:
- Trim and lowercase email before storage.
- Validate email syntax.
- Password length: 8–128 bytes.
- Store only an Argon2id password hash (64 MiB memory, 3 iterations, 4 lanes in the current implementation).
- Duplicate normalized email returns 409.

Response — 201 Created:

~~~json
{
  "user": {
    "id": "9a1f7e5d-8c14-4d2c-9f16-8a8e59db43b1",
    "email": "dev@example.com",
    "created_at": "2026-10-31T16:00:00Z"
  },
  "access_token": "<signed-token>",
  "token_type": "Bearer",
  "expires_in": 900
}
~~~

Errors: 400 invalid input, 409 email already registered, 500 unexpected failure.

## 4. Log in

~~~http
POST /api/v1/auth/login
Content-Type: application/json
~~~

Request:

~~~json
{
  "email": "dev@example.com",
  "password": "correct horse battery staple"
}
~~~

Response — 200 OK:

~~~json
{
  "user": {
    "id": "9a1f7e5d-8c14-4d2c-9f16-8a8e59db43b1",
    "email": "dev@example.com"
  },
  "access_token": "<signed-token>",
  "token_type": "Bearer",
  "expires_in": 900
}
~~~

Use the same generic 401 response for an unknown email and a wrong password to avoid account enumeration. Errors: 400 malformed request, 401 invalid credentials, 500 unexpected failure.

## 5. Create a short URL

~~~http
POST /api/v1/urls
Authorization: Bearer <access_token>
Content-Type: application/json
~~~

Request:

~~~json
{
  "url": "https://example.com/articles/hello?source=docs",
  "custom_alias": "hello-world",
  "expires_at": "2026-12-31T23:59:59Z"
}
~~~

custom_alias and expires_at are optional. Omit custom_alias to generate a random code. Omit expires_at for a link that does not expire.

Response — 201 Created:

~~~json
{
  "id": "a6b4c98d-14c2-45fd-a915-86e54ba34f1f",
  "short_code": "hello-world",
  "short_url": "https://sho.rt/hello-world",
  "original_url": "https://example.com/articles/hello?source=docs",
  "created_at": "2026-10-31T16:00:00Z",
  "expires_at": "2026-12-31T23:59:59Z"
}
~~~

For generated codes, short_code is a 10-character, case-sensitive Base62 value.

Errors:
- 400 invalid JSON, invalid destination URL, invalid alias, or invalid/past expiry.
- 401 missing or invalid access token.
- 409 custom alias already exists.
- 500 unexpected failure.

## 6. List the authenticated user's URLs

~~~http
GET /api/v1/urls?limit=20&cursor=<opaque-cursor>
Authorization: Bearer <access_token>
~~~

- Default limit: 20.
- Maximum limit: 100.
- Results are ordered by created_at descending, with id as a stable tie-breaker.
- cursor is opaque to clients and supports keyset pagination.
- Return active and inactive URLs so the owner can see their history; expired links remain visible.

Response — 200 OK:

~~~json
{
  "items": [
    {
      "id": "a6b4c98d-14c2-45fd-a915-86e54ba34f1f",
      "short_code": "hello-world",
      "short_url": "https://sho.rt/hello-world",
      "original_url": "https://example.com/articles/hello?source=docs",
      "created_at": "2026-10-31T16:00:00Z",
      "expires_at": "2026-12-31T23:59:59Z",
      "is_active": true
    }
  ],
  "next_cursor": null
}
~~~

Errors: 400 invalid limit/cursor, 401 unauthenticated, 500 unexpected failure.

## 7. Read URL metadata

~~~http
GET /api/v1/urls/{code}
Authorization: Bearer <access_token>
~~~

Response — 200 OK:

~~~json
{
  "id": "a6b4c98d-14c2-45fd-a915-86e54ba34f1f",
  "short_code": "hello-world",
  "short_url": "https://sho.rt/hello-world",
  "original_url": "https://example.com/articles/hello?source=docs",
  "created_at": "2026-10-31T16:00:00Z",
  "expires_at": "2026-12-31T23:59:59Z",
  "is_active": true
}
~~~

Errors: 401 unauthenticated, 404 unknown or not owned, 500 unexpected failure. An expired link remains readable by its owner as metadata.

## 8. Read URL statistics

~~~http
GET /api/v1/urls/{code}/stats?from=2026-10-01&to=2026-10-31
Authorization: Bearer <access_token>
~~~

- from and to are optional UTC calendar dates.
- If omitted, return all available history.
- If supplied, the range is inclusive and from must not be after to.
- Maximum requested range: 90 calendar days.
- A click means a request that resolves to an active, unexpired link and receives a 302 response. It is not a count of unique people.
- Analytics are best-effort in the MVP; recent events may take a short time to appear.

Response — 200 OK:

~~~json
{
  "short_code": "hello-world",
  "total_clicks": 123,
  "daily_clicks": [
    {
      "date": "2026-10-30",
      "clicks": 12
    },
    {
      "date": "2026-10-31",
      "clicks": 18
    }
  ]
}
~~~

Only aggregate counts are retained. Do not expose or collect raw IP addresses in the MVP.

Errors: 400 invalid date/range, 401 unauthenticated, 404 unknown or not owned, 500 unexpected failure.

## 9. Deactivate a URL

~~~http
DELETE /api/v1/urls/{code}
Authorization: Bearer <access_token>
~~~

Deactivation is a soft state change. The short code must stop redirecting; the row remains available to the owner as inactive metadata.

Response — 204 No Content.

Errors: 401 unauthenticated, 404 unknown or not owned, 500 unexpected failure.

## 10. Public redirect

~~~http
GET /{code}
~~~

No authentication required.

For an active, unexpired code:

~~~http
HTTP/1.1 302 Found
Location: https://example.com/articles/hello?source=docs
Cache-Control: no-store
~~~

Behavior:
- Unknown code: 404 Not Found.
- Inactive code: 404 Not Found.
- Expired code: 410 Gone.
- Redis unavailable: fall back to PostgreSQL; the cache is not required for service startup or readiness.
- Redis cache TTL: at most five minutes and never later than the link's absolute expires_at.
- Expiry is checked on every cache hit, so an old cache entry cannot redirect an expired link.
- Cache keys use the versioned form `short-url:v1:{code}`. Unknown, inactive, and expired links are not cached.
- The redirect service invokes an enqueue-only click-recorder interface. The current runtime uses a no-op adapter; Issue #7 will add the bounded queue and durable aggregate updates.
- Do not use 301; the MVP uses 302 so link targets remain changeable and browser caches do not make the redirect permanent.
- The service returns a redirect; it does not fetch or proxy the destination.

## 11. Health and readiness

### Liveness

~~~http
GET /healthz
~~~

Response — 200 OK:

~~~json
{
  "status": "ok"
}
~~~

This reports that the process is running. It should not fail just because a dependency is temporarily unavailable.

### Readiness

~~~http
GET /readyz
~~~

Response — 200 OK:

~~~json
{
  "status": "ready",
  "dependencies": {
    "postgres": "ok",
    "redis": "degraded"
  }
}
~~~

PostgreSQL is required for readiness. Redis is optional for core functionality because the service can fall back to PostgreSQL. Return 503 when PostgreSQL cannot serve required operations.

## 12. Status-code reference

| Status | Use |
|---|---|
| 200 | Successful read/login |
| 201 | Account or URL created |
| 204 | URL deactivated |
| 302 | Redirect |
| 400 | Invalid request or validation error |
| 401 | Authentication missing or invalid |
| 404 | Unknown/inactive/not-owned resource |
| 409 | Duplicate email or alias |
| 410 | Expired short link |
| 429 | Rate limited (added in hardening milestone) |
| 500 | Unexpected server error |
| 503 | Required service unavailable |

## 13. Contract implementation notes

- Register specific API routes before the one-segment public redirect route, or use method-aware ServeMux patterns so route precedence is unambiguous.
- Enforce ownership in repository queries, not only in handler code.
- Use a configured PUBLIC_BASE_URL to generate returned short_url values.
- Limit request body size and reject unknown JSON fields for write endpoints.
- Return consistent error bodies for API routes; the redirect endpoint uses normal HTTP redirect/error semantics.
