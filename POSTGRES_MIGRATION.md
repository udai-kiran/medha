# PostgreSQL Migration Plan

## Overview
Migration from SQLite3 to PostgreSQL requires changes to:
1. Database driver (modernc.org/sqlite → lib/pq)
2. Connection string format
3. SQL dialect compatibility fixes
4. Configuration management
5. Docker/deployment setup
6. Schema migrations

## Changes Required

### 1. Go Module Dependencies
**File:** `medha-api/go.mod`
- Remove: `modernc.org/sqlite v1.50.1`
- Add: `github.com/lib/pq` (PostgreSQL driver)
- Update imports in `medha-api/internal/state/sqlite.go` → `postgres.go`

### 2. Database Driver & Connection
**Files affected:**
- `medha-api/internal/state/sqlite.go` (rename to `postgres.go`)
- `medha-api/internal/state/kv.go` (SQL syntax updates)
- `medha-api/internal/state/schema.go` (migration updates)

**Changes:**
- Replace `_ "modernc.org/sqlite"` with `_ "github.com/lib/pq"`
- Update `sql.Open()` call from `"sqlite"` to `"postgres"`
- Change DSN format from file path to PostgreSQL connection string
- Remove SQLite-specific pragmas (WAL, busy timeout, synchronous)
- Replace SQLite-specific SQL syntax

### 3. SQL Dialect Compatibility

**String concatenation:**
- SQLite: `||` → PostgreSQL: `||` (same, no change)

**UPSERT syntax:**
```go
// SQLite
INSERT INTO kv (scope, key, value_json, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(scope, key) DO UPDATE SET value_json = excluded.value_json, ...

// PostgreSQL
INSERT INTO kv (scope, key, value_json, updated_at) VALUES ($1, $2, $3, $4)
ON CONFLICT(scope, key) DO UPDATE SET value_json = excluded.value_json, ...
```

**Placeholder parameters:**
- SQLite uses `?` 
- PostgreSQL uses `$1, $2, $3...`
- Update all query placeholders in:
  - `kv.go` (Get, Put, Delete, ListByPrefix)
  - `crud.go` (observation/session/memory queries)
  - `orchestration.go` (action/lease/routine/signal queries)
  - All test files

**Auto-increment primary keys:**
- SQLite: AUTOINCREMENT (rarely needed)
- PostgreSQL: Use SERIAL or UUID

**Type considerations:**
- SQLite: All dates as TEXT (ISO-8601)
- PostgreSQL: Can use TIMESTAMP, but TEXT is also fine (keep existing for consistency)
- SQLite: BLOB handling
- PostgreSQL: BYTEA type (if needed)

### 4. Configuration
**File:** `medha-api/internal/config/config.go`

Replace:
```go
SQLitePath string
```

With:
```go
// PostgreSQL connection parameters
PostgresHost     string
PostgresPort     int
PostgresUser     string
PostgresPassword string
PostgresDB       string
PostgresSSLMode  string // disable, require, prefer
```

Or simpler: Single `PostgresDSN` string

### 5. Schema Migrations
**File:** `medha-api/internal/state/schema.go`

Changes needed:
- Add migration version for PostgreSQL-specific schema (or keep schema dialect-agnostic)
- Update migrations that use SQLite-specific functions:
  - `json_extract()` → `->` operator (PostgreSQL JSON functions)
  - `json_each()` → `jsonb_array_elements()`
  - `typeof()` → `pg_typeof()`

### 6. Connection Pool & Options
**File:** `medha-api/internal/state/postgres.go`

Add PostgreSQL-specific options:
```go
type Options struct {
    DSN              string        // Full PostgreSQL DSN
    MaxOpenConns     int           // Default: 25
    MaxIdleConns     int           // Default: 5
    ConnMaxLifetime  time.Duration // Default: 5 min
}
```

### 7. Docker & Environment
**Files affected:**
- `docker-compose.yml` - Add PostgreSQL service
- `.env.example` - Update/add PostgreSQL variables
- `Makefile` - Update example commands
- `docs/DEVELOPMENT.md` - Update setup instructions

**Environment variables needed:**
```
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=medha
POSTGRES_PASSWORD=changeme
POSTGRES_DB=medha_dev
POSTGRES_SSLMODE=disable
```

### 8. Documentation
- Update README.md to mention PostgreSQL support
- Update DEVELOPMENT.md with PostgreSQL setup
- Add migration guide in docs/

## Implementation Steps

1. **Phase 1: Prepare driver layer**
   - Update `go.mod` with PostgreSQL driver
   - Create `postgres.go` replacing `sqlite.go`
   - Update config to accept PostgreSQL connection params

2. **Phase 2: SQL compatibility**
   - Update all placeholder parameters (? → $1, $2, etc.)
   - Update UPSERT syntax if needed
   - Test each file: kv.go, crud.go, orchestration.go

3. **Phase 3: Schema & migrations**
   - Keep schema migration agnostic where possible
   - Update schema.go migrations if needed
   - Update schema.go version/migration tracking

4. **Phase 4: Integration & testing**
   - Update tests to use PostgreSQL
   - Docker Compose setup with PostgreSQL
   - End-to-end testing
   - Performance testing vs SQLite

5. **Phase 5: Documentation**
   - Update README, setup docs
   - Migration guide for existing deployments
   - Deployment instructions

## Backward Compatibility

**Recommendation:** Not maintain SQLite support during migration
- Remove SQLite driver completely
- Simplify codebase (no need for database abstraction layer)
- Forces clean migration

Alternatively, to support both:
- Use database/sql interface (already using it)
- Create `Store` factory that opens either driver
- Keep migration compatibility for both

## Testing Strategy

1. **Unit tests:** Update all state package tests to use PostgreSQL
2. **Integration tests:** E2E tests with full stack
3. **Performance tests:** Compare SQLite vs PostgreSQL performance
4. **Migration tests:** Test schema migrations on fresh DB

## Deployment Considerations

- PostgreSQL instance must be running (separate service or managed database)
- Connection pooling configuration
- Backup/restore procedures
- Monitoring and metrics
- Auto-scaling if using cloud database
