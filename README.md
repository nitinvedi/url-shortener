# URL Shortener

A production-minded URL shortener built with Go, PostgreSQL, and Redis.

The project is being developed incrementally, starting with a reliable shortening and redirect flow, then adding authentication, ownership, analytics, and production operations.

## Project plan

See **[ROADMAP.md](./ROADMAP.md)** for the architecture, API sketch, delivery milestones, repository structure, engineering rules, and links to the implementation issues.

## Planned stack

- **Go 1.23** — HTTP service
- **PostgreSQL** — durable source of truth
- **Redis** — redirect cache and rate-limit state
- **Docker Compose** — local development
- **GitHub Actions** — continuous integration

## Current status

Architecture and API design are documented in [docs/architecture.md](./docs/architecture.md) and [docs/api.md](./docs/api.md). The next implementation steps are the Go service foundation and PostgreSQL layer, followed by authentication before owner-scoped URL creation.

## Development approach

Work through the GitHub issues in milestone order. Keep changes focused, add tests alongside features, and use pull requests for review.
