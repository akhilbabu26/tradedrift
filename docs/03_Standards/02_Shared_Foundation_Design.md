# TradeDrift — Shared Foundation Design (Go Monorepo)

> **Status:** ✅ Frozen (V1.0)
> **Document:** 02_Shared_Foundation_Design.md
> **Directory:** docs/03_Standards/
> **Last Updated:** July 2026

---

## 1. Purpose

This document specifies the architecture, package boundaries, code interfaces, and compile-time automation for the TradeDrift **Shared Foundation** library (`platform` module). 

To ensure consistent implementation of platform invariants across all microservices, developers must import these standard packages instead of writing ad-hoc logic for config, database, caches, logging, serialization, UUID generation, outbox loops, and session token validation.

---

## 2. Monorepo Directory Topology

TradeDrift operates as a Go **Multi-Module Monorepo** using native Go Workspaces (`go.work`). This eliminates the need for local `replace` directives in service `go.mod` files.

```
tradedrift/
  ├── go.work                         # Multi-module workspace definition
  ├── proto/                          # Canonical protobuf contracts (Frozen)
  │     ├── common/v1/common.proto
  │     ├── auth/v1/auth.proto
  │     ├── wallet/v1/wallet.proto
  │     ├── order/v1/order.proto
  │     └── admin/v1/admin.proto
  ├── platform/                       # Shared platform library module
  │     ├── go.mod
  │     ├── api/                      # Compiled Go protobuf client/server stubs
  │     │     ├── gen/                # Output generated files
  │     │     └── Makefile            # Cross-platform protobuf build script
  │     ├── config/                   # Reserved: Configuration loader package
  │     ├── database/                 # Reserved: Database connection setup & poolers
  │     ├── redis/                    # Reserved: Redis client & Sentinel connections
  │     ├── kafka/                    # Reserved: Kafka producer/consumer utilities
  │     ├── logger/                   # Reserved: Structured logger engine
  │     ├── errors/                   # Reserved: Domain errors mapping package
  │     ├── uuid/                     # Standard UUIDv7 generator package
  │     │     └── uuid.go
  │     ├── outbox/                   # Reusable transactional outbox engine
  │     │     ├── publisher.go
  │     │     └── serializer.go       # Abstract serialization layer
  │     └── jwt/                      # JWT validation packages
  │           ├── validator/          # Generic JWT signature/blacklist validation
  │           │     └── validator.go
  │           └── transport/          # Transport-specific adapters
  │                 ├── grpc/
  │                 │     └── interceptor.go
  │                 └── http/
  │                       └── middleware.go
```

---

## 3. Go Workspace Configuration (`go.work`)

The root `go.work` file must register the platform module:

```go
go 1.21

use (
	./platform
	// Service modules register here as they are added:
	// ./services/auth
	// ./services/wallet
)
```

---

## 4. Protobuf Compilation Standard (`platform/api`)

To prevent version skew between services, protobuf stubs must be compiled centrally within the `platform` module. 

### 4.1 Dependency Directory Mapping
Services import compiled stubs via the `platform` module path:
```go
import (
    orderv1 "tradedrift/platform/api/gen/order/v1"
    walletv1 "tradedrift/platform/api/gen/wallet/v1"
)
```

### 4.2 Cross-Platform Build Automation (`platform/api/Makefile`)
Developers in both Windows (using make/git-bash) and Linux must compile protos using the standard `Makefile`:

```makefile
.PHONY: all compile clean

PROTO_DIR=../../proto
GEN_DIR=gen

all: compile

compile:
	@mkdir -p $(GEN_DIR)
	protoc --proto_path=$(PROTO_DIR) \
	       --go_out=$(GEN_DIR) --go_opt=paths=source_relative \
	       --go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
	       $(PROTO_DIR)/common/v1/common.proto \
	       $(PROTO_DIR)/auth/v1/auth.proto \
	       $(PROTO_DIR)/wallet/v1/wallet.proto \
	       $(PROTO_DIR)/order/v1/order.proto \
	       $(PROTO_DIR)/admin/v1/admin.proto

clean:
	rm -rf $(GEN_DIR)/*
```

---

## 5. UUIDv7 Standard Generator (`platform/uuid`)

This package provides a standardized UUIDv7 generator conforming to `ID_Correlation_Standard.md`. UUIDv7 generators must use a millisecond-precision unix timestamp combined with cryptographically secure random bits.

### 5.1 Go API
```go
package uuid

import (
	"github.com/google/uuid"
)

// NewV7 generates a standard UUIDv7 identifier.
// Returns an error if the cryptographically secure random source fails.
func NewV7() (uuid.UUID, error) {
	return uuid.NewV7()
}
```

---

## 6. Transactional Outbox Engine (`platform/outbox`)

The `platform/outbox` package provides a generic, reusable publisher loop utilizing database-level concurrency controls and abstract serialization formats.

### 6.1 Serialization Abstraction (`platform/outbox/serializer.go`)
To avoid hard-coupling the database outbox payload storage format to JSON, we define a serialization abstraction interface:

```go
package outbox

// Serializer manages outbox payload encoding/decoding.
type Serializer interface {
	Marshal(v interface{}) ([]byte, error)
	Unmarshal(data []byte, v interface{}) error
}
```

### 6.2 Event Definition
```go
package outbox

import "time"

type Event struct {
	ID           [16]byte  // UUIDv7 event identifier
	AggregateID  [16]byte  // Target aggregate identifier
	EventType    string    // Versioned event name, e.g. "orders.created.v1"
	Payload      []byte    // Serialized payload bytes
	PartitionKey string    // Kafka routing key
	CreatedAt    time.Time
}
```

### 6.3 Polling & Leasing Algorithm
The publisher uses transaction leases to scale horizontally without duplicate publishing. The core query uses `FOR UPDATE SKIP LOCKED` row locking:

```sql
SELECT id, aggregate_id, event_type, payload, partition_key 
FROM outbox 
WHERE status = 'PENDING' 
ORDER BY created_at ASC 
LIMIT $1 
FOR UPDATE SKIP LOCKED;
```

### 6.4 Publisher Interface
```go
package outbox

import (
	"context"
	"database/sql"
)

type KafkaProducer interface {
	PublishSync(ctx context.Context, topic string, key string, value []byte) error
}

type Engine struct {
	db         *sql.DB
	producer   KafkaProducer
	serializer Serializer
	batchSize  int
}

func NewEngine(db *sql.DB, producer KafkaProducer, serializer Serializer, batchSize int) *Engine {
	return &Engine{
		db:         db,
		producer:   producer,
		serializer: serializer,
		batchSize:  batchSize,
	}
}
```

---

## 7. JWT Validation Standard (`platform/jwt`)

JWT verification functions are split into core validator logic (generic) and transport adapters (transport-specific) to keep validation decoupled from request delivery protocols.

### 7.1 Generic Validator (`platform/jwt/validator/validator.go`)
```go
package validator

import "context"

type TokenClaims struct {
	UserID    string
	SessionID string
	Email     string
}

type Validator interface {
	// VerifyToken parses, validates signatures, and queries the Redis blacklist database.
	VerifyToken(ctx context.Context, tokenStr string) (*TokenClaims, error)
}
```

### 7.2 Transport Adapters (`platform/jwt/transport/`)

* **gRPC Interceptor (`platform/jwt/transport/grpc/interceptor.go`):** Reads JWT credentials from incoming RPC metadata context.
* **HTTP Middleware (`platform/jwt/transport/http/middleware.go`):** Reads JWT credentials from the HTTP Authorization header (used in NGINX gateway and auth routers).

---

## 8. Engineering Dependency Constraints

To maintain a clean architectural boundary, the TradeDrift codebase enforces strict **unidirectional dependency boundaries**:

```
 ┌──────────────────────────────────────────────┐
 │             Service Modules                  │
 │   (e.g., auth, wallet, order, settlement)   │
 └──────────────────────┬───────────────────────┘
                        │ (Imports platform)
                        ▼
 ┌──────────────────────────────────────────────┐
 │             Platform Module                  │
 │ (config, database, outbox, jwt, uuid, api)   │
 └──────────────────────────────────────────────┘
```

1. **Service-to-Platform:** Service modules may import any package inside the `platform` module.
2. **Platform-to-Service:** The `platform` module **must have zero imports** referencing any service module. The platform package is entirely generic and self-contained.
3. **Circular Reference Control:** No circular imports are allowed between service modules (e.g. `order` service must not import `wallet` service packages; all cross-service coordination happens via gRPC clients or Kafka events).
