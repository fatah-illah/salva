# Multi-Tenant Messaging System

![Go](https://img.shields.io/badge/Go-1.19%2B-blue)
![RabbitMQ](https://img.shields.io/badge/RabbitMQ-3.9%2B-orange)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-13%2B-blueviolet)
![License](https://img.shields.io/badge/License-MIT-green)

A scalable multi-tenant messaging system built with Go, RabbitMQ, and PostgreSQL. The system provides dynamic tenant management, partitioned data storage, configurable concurrency, and graceful shutdown capabilities.

## Features

- 🚀 **Auto-Spawn Tenant Consumers**: Creates dedicated queues and consumers per tenant
- ⏹️ **Auto-Stop Tenant Consumers**: Gracefully terminates tenant-specific resources
- 🗄️ **Partitioned Message Storage**: PostgreSQL table partitioning by tenant ID
- ⚙️ **Configurable Concurrency**: Dynamic worker pool adjustment per tenant
- ✨ **Graceful Shutdown**: Ensures completion of in-progress operations
- 📖 **Cursor Pagination**: Efficient pagination for message retrieval
- 📚 **Swagger Documentation**: Auto-generated API documentation
- 🧪 **Integration Tests**: Comprehensive tests using dockertest
- 🔒 **Security**: JWT authentication (optional implementation)

## Architecture Overview

```mermaid
graph TD
    A[API Client] --> B[HTTP API]
    B --> C[Tenant Manager]
    C --> D[RabbitMQ]
    C --> E[PostgreSQL]
    D --> F[Tenant Queue 1]
    D --> G[Tenant Queue 2]
    D --> H[Tenant Queue N]
    F --> I[Worker Pool 1]
    G --> J[Worker Pool 2]
    H --> K[Worker Pool N]
    I --> E
    J --> E
    K --> E
```

## Prerequisites

- Go 1.19+
- Docker
- PostgreSQL 13+
- RabbitMQ 3.9+

## Installation

1. **Clone the repository**:
```bash
git clone https://github.com/your-username/multi-tenant-messaging.git
cd multi-tenant-messaging
```

2. **Install dependencies**:
```bash
go mod tidy
```

3. **Set up environment**:
```bash
cp configs/config.example.yaml configs/config.yaml
```

4. **Update configuration** (Edit `configs/config.yaml`):
```yaml
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
database:
  url: "postgres://postgres:postgres@localhost:5432/multi_tenant?sslmode=disable"
workers: 3
server:
  port: ":8080"
```

## Running the Application

### Start Dependencies
```bash
docker-compose up -d
```

### Run Migrations
```bash
go run cmd/server/main.go -migrate
```

### Start the Server
```bash
go run cmd/server/main.go
```

## API Endpoints

### Tenant Management
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/tenants` | POST | Create a new tenant |
| `/tenants/{id}` | DELETE | Delete a tenant |
| `/tenants/{id}/config/concurrency` | PUT | Update worker concurrency |

### Message Retrieval
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/messages` | GET | List messages with cursor pagination |

### Swagger Documentation
Access API documentation at: `http://localhost:8080/swagger/index.html`

## Running Tests

### Unit Tests
```bash
go test ./...
```

### Integration Tests
```bash
go test -tags=integration ./...
```

## Configuration Options

| Key | Default | Description |
|-----|---------|-------------|
| `rabbitmq.url` | `amqp://guest:guest@localhost:5672/` | RabbitMQ connection URL |
| `database.url` | `postgres://postgres:postgres@localhost:5432/multi_tenant?sslmode=disable` | PostgreSQL connection URL |
| `workers` | `3` | Default worker count per tenant |
| `server.port` | `:8080` | HTTP server port |

## Graceful Shutdown

The application supports graceful shutdown:
1. Press `Ctrl+C` to initiate shutdown
2. The server will:
   - Stop accepting new requests
   - Complete in-progress operations
   - Close database and RabbitMQ connections
   - Terminate worker goroutines
3. Shutdown completes within 30 seconds

## Monitoring

Prometheus metrics are available at `/metrics`:
- `tenant_workers_current`: Current worker count per tenant
- `queue_messages_pending`: Pending messages per tenant queue
- `messages_processed_total`: Total messages processed

## Additional Features

### Dead Letter Queues
Failed messages are automatically routed to a dead letter exchange (`dlx_exchange`) for reprocessing.

### Security
For production deployments, enable JWT authentication by setting:
```yaml
security:
  jwt_secret: "your-strong-secret-key"
```

## Deployment

### Docker Build
```bash
docker build -t multi-tenant-messaging .
```

### Docker Run
```bash
docker run -p 8080:8080 \
  -e RABBITMQ_URL=amqp://rabbit:5672 \
  -e DATABASE_URL=postgres://user:pass@db:5432/app \
  multi-tenant-messaging
```

## Contributing

Contributions are welcome! Please follow these steps:
1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Commit your changes (`git commit -am 'Add some feature'`)
4. Push to the branch (`git push origin feature/your-feature`)
5. Open a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.