# PostgreSQL Setup Guide

Medha now uses PostgreSQL instead of SQLite3 for persistence. This guide covers setup options.

## Quick Start

### Option 1: Embedded PostgreSQL (Local Development)

Start the full stack with embedded PostgreSQL:

```bash
# Copy and configure environment
cp .env.example .env

# Start services including embedded PostgreSQL
docker compose --profile postgres up --build
```

Services will be available at:
- **API**: http://localhost:3111
- **MCP**: stdio (configure agent host)
- **Viewer**: http://localhost:3113
- **Python extraction**: http://localhost:5000
- **PostgreSQL**: localhost:5432 (credentials in .env.example)

### Option 2: External PostgreSQL

Use your own PostgreSQL instance (production setup):

```bash
# Configure external PostgreSQL in .env
POSTGRES_HOST=your-db-host.com
POSTGRES_PORT=5432
POSTGRES_USER=medha_user
POSTGRES_PASSWORD=secure-password
POSTGRES_DB=medha_prod
POSTGRES_SSLMODE=require  # or disable for local dev

# Start services (PostgreSQL service will not be included)
docker compose up --build
```

### Option 3: Local Development (without Docker)

For native development on your machine:

```bash
# Prerequisite: PostgreSQL installed locally
# Create database and user:
createdb medha
createuser medha
psql medha -c "ALTER USER medha WITH PASSWORD 'password';"

# Configure .env
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=medha
POSTGRES_PASSWORD=password
POSTGRES_DB=medha
POSTGRES_SSLMODE=disable

# Install Go and Python deps
make setup

# Run services locally
make run-go  # Terminal 1
make run-py  # Terminal 2
```

## Configuration

### Environment Variables

All PostgreSQL options are configured via environment variables:

| Variable | Default | Notes |
|----------|---------|-------|
| `POSTGRES_HOST` | localhost | Database hostname or IP |
| `POSTGRES_PORT` | 5432 | Database port |
| `POSTGRES_USER` | medha | Database user |
| `POSTGRES_PASSWORD` | (required) | Database password |
| `POSTGRES_DB` | medha | Database name |
| `POSTGRES_SSLMODE` | disable | SSL mode: disable, require, prefer |

### SSL/TLS for Production

```bash
# For production with SSL:
POSTGRES_SSLMODE=require
POSTGRES_SSLCERT=/path/to/cert.pem
POSTGRES_SSLKEY=/path/to/key.pem
POSTGRES_SSLROOTCERT=/path/to/ca.pem
```

## Docker Compose Profiles

Medha uses Docker Compose profiles for flexible deployment:

### Default (No profiles)
```bash
docker compose up
# Includes: Go API, Python extraction, RabbitMQ
# Requires: External PostgreSQL via POSTGRES_* env vars
```

### With Embedded PostgreSQL
```bash
docker compose --profile postgres up
# Includes: All above + PostgreSQL service
```

### With Neo4j (Optional Graph DB)
```bash
docker compose --profile neo4j up
# Includes: Go API, Python extraction, RabbitMQ, Neo4j
# Note: Go service operates in degraded mode without Neo4j per ADR-0003
```

### Full Stack
```bash
docker compose --profile postgres --profile neo4j up
# Includes: Everything
```

## Connection Pooling

The Go service automatically manages connection pooling:

- **Max Open Connections**: 25 (configurable if needed)
- **Max Idle Connections**: 5
- **Connection Max Lifetime**: 5 minutes

For tuning, edit `medha-api/internal/state/postgres.go` Options struct.

## Schema Migrations

Migrations are applied automatically on startup. The state package tracks the schema version:

```
2026-05-26: v1 - Core tables (sessions, observations, memories, KV)
```

To view applied migrations:
```bash
psql medha -c "SELECT * FROM migrations_applied ORDER BY version;"
```

## Backup and Restore

### Backup Database

```bash
# Using pg_dump
pg_dump -U medha -h localhost medha > backup.sql

# Compressed backup
pg_dump -U medha -h localhost medha | gzip > backup.sql.gz
```

### Restore Database

```bash
# From plain SQL
psql -U medha -h localhost -d medha < backup.sql

# From compressed backup
gunzip < backup.sql.gz | psql -U medha -h localhost -d medha
```

### With Docker

```bash
# Backup from running container
docker compose exec postgres pg_dump -U medha medha > backup.sql

# Restore to running container
docker compose exec -T postgres psql -U medha medha < backup.sql
```

## Troubleshooting

### Connection Refused

```bash
# Verify PostgreSQL is running
docker compose ps postgres
# or locally: pg_isready -h localhost -p 5432

# Check credentials
psql -U medha -h localhost -d medha
```

### Slow Queries

Check indices:
```bash
psql medha -c "\di"
```

View active queries:
```bash
psql medha -c "SELECT pid, query FROM pg_stat_activity WHERE query NOT ILIKE '%pg_stat%';"
```

### Out of Connections

Check connection count:
```bash
psql medha -c "SELECT count(*) FROM pg_stat_activity;"
```

### SSL Certificate Errors

For self-signed certificates:
```bash
POSTGRES_SSLMODE=require  # Accept any certificate
# or
POSTGRES_SSLMODE=verify-ca  # With specific CA cert
```

## Performance Tips

1. **Indices**: The schema includes indices on frequently-queried columns (project, created_at, etc.)
2. **Connection Pool**: Adjust MaxOpenConns if you need higher concurrency
3. **AUTOVACUUM**: PostgreSQL handles this automatically; monitor with:
   ```bash
   psql medha -c "SELECT * FROM pg_stat_user_tables WHERE n_live_tup > 10000;"
   ```

## Migration from SQLite

If you previously used SQLite:

1. **Export data from SQLite**:
   ```bash
   sqlite3 data/agentmemory.db ".dump" > sqlite_dump.sql
   ```

2. **Adapt schema** (SQLite and PostgreSQL have slight differences):
   - Remove SQLite pragmas
   - Update data types if needed
   - Test on PostgreSQL

3. **Import to PostgreSQL**:
   ```bash
   # Note: Dump format may need manual adaptation
   psql medha < sqlite_dump.sql
   ```

For large migrations, consider using a tool like `pgloader`:
```bash
pgloader sqlite:///path/to/db.db postgresql:///medha
```

## References

- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
- [PostgreSQL Docker Image](https://hub.docker.com/_/postgres/)
- [lib/pq Go Driver](https://github.com/lib/pq)
- [Medha ADR-0003: Neo4j Optional](../docs/ADRs/0003-neo4j-optional.md)
