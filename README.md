# Multi-Tenant Messaging System

A Go application for multi-tenant messaging using RabbitMQ and PostgreSQL.

## Features
- Dynamic consumer per tenant (auto-spawn/stop)
- Partitioned message table per tenant
- Configurable worker pool concurrency
- Graceful shutdown
- Cursor pagination API
- JWT authentication
- Prometheus monitoring
- Dead-letter queue (DLQ) for failed messages
- Integration tests with dockertest
- OpenAPI/Swagger documentation

## Quick Start

### 1. Clone & Setup
```sh
git clone <repo-url>
cd salva
cp config/config.yaml.example config/config.yaml
```

### 2. Start Dependencies (PostgreSQL & RabbitMQ)
```sh
docker-compose up -d
```

### 3. Run DB Migration
Masuk ke container atau gunakan psql:
```sh
psql -h localhost -U user -d app
# Lalu jalankan:
CREATE TABLE messages (
  id UUID,
  tenant_id UUID NOT NULL,
  payload JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  PRIMARY KEY (tenant_id, id)
) PARTITION BY LIST (tenant_id);
```

### 4. Run App
```sh
go run ./cmd/main.go
```

### 5. API Usage
- POST /tenants?id=... (JWT required)
- DELETE /tenants?id=... (JWT required)
- PUT /tenants/config/concurrency?id=... (JWT required)
- GET /messages?cursor=... (JWT required)

### 6. Monitoring & Docs
- Prometheus: http://localhost:2112/metrics
- RabbitMQ UI: http://localhost:15672 (user/pass)
- Swagger: generate with `swag init` (see docs/)

### 7. Integration Test
```sh
go test ./test/...
```

## Notes
- Set JWT secret di config.yaml
- Setiap tenant baru, buat partisi tabel messages:
  ```sql
  CREATE TABLE messages_tenant_<tenant_id> PARTITION OF messages FOR VALUES IN ('<tenant_id>');
  ```
- Untuk production, gunakan env var untuk config sensitif.
