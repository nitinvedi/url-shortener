# Database migrations

Migrations are embedded into the Go binary and applied at application startup.

## Naming

Use monotonically increasing names in the format:

    000002_add_url_title.up.sql

## Safety rules

- Never edit a migration after it has been applied. The migration runner stores a SHA-256 checksum and rejects a changed file.
- Add a new migration to correct or extend an existing schema.
- Migrations run in their own transaction, in numeric order, while holding a PostgreSQL advisory lock.
- The MVP uses a forward-only migration policy. There are no automatic down migrations. For destructive changes, take a backup and plan a forward repair or restore procedure before rollout.
- Test every migration from an empty PostgreSQL database and against a database at the previous version.
