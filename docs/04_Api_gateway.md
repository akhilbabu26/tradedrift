# TradeDrift — API Gateway

> **Status:** ✅ Designed

## Purpose

The API Gateway is the single entry point for all client traffic. It handles logging/metrics, rate limiting, route resolution, and authentication enforcement before forwarding requests to downstream services over gRPC. No client ever calls a backend service directly.

## Responsibilities

- Accept all incoming HTTP requests from web/mobile clients.
- Record request timing and counters (logging/metrics).
- Enforce per-client rate limiting (Redis token bucket).
- Resolve the target service and whether the route requires authentication.
- Delegate JWT validation to the shared verification middleware when a route requires auth.
- Forward validated requests to the correct downstream service via gRPC.

## Out of Scope

- Does not implement business logic for any downstream service.
- Does not issue or revoke JWTs — that's owned by Authentication Service (the Gateway only *validates* tokens via shared middleware).
- Does not persist any data of its own beyond transient rate-limit counters in Redis.

## Request Pipeline

```
Incoming HTTP request (from client)
  ↓
Logging / metrics (request timing and counters)
  ↓
Rate limit middleware (Redis token bucket)
  ├── Over limit → Rejected: 429 response
  ↓
Route resolution (target service + auth flag)
  ↓
Requires auth?
  ├── No  → gRPC client → forwards to target service
  └── Yes → JWT middleware (signature, expiry, revocation)
              ├── Invalid → Rejected: 401 response
              └── Valid   → gRPC client → forwards to target service
  ↓
Downstream service (Auth Service / Order Service / Wallet Service / Market Service / ...)
```

See `images/api_gateway_pipeline.png` for the full flow diagram.

## Middleware Order (fixed, always in this sequence)

1. **CORS middleware** — handles preflight (`OPTIONS`) requests and sets `Access-Control-Allow-Origin`, `Access-Control-Allow-Headers`, `Access-Control-Allow-Methods`. Applied first so preflight responses skip rate limiting and auth.
2. **Logging / metrics** — applied to every request, pass or fail, so failed requests are still observable.
3. **Rate limit middleware** — Redis token bucket, checked before auth so unauthenticated flooding is rejected cheaply, without paying the cost of JWT verification.
4. **Route resolution** — determines which downstream service owns this path, and whether the route is public or requires a valid token.
5. **JWT middleware** (conditional) — only runs when route resolution flags the route as requiring auth.
6. **gRPC client** — forwards to the resolved downstream service with a **configurable timeout** (default 5s). A circuit breaker prevents cascading failures when a downstream service is consistently unresponsive.

## Shared JWT Validation Logic

> **Decision:** JWT middleware here and Authentication Service's own JWT Validation Flow are backed by the *same* shared internal package — not two independent implementations. See `05_Authentication_Service.md`, Section 10, for the full rationale. If the two implementations diverged, a token accepted by one and rejected by the other would be a serious, hard-to-diagnose bug.

## Downstream Services (current)

- Auth Service
- Order Service
- Market Service
- *(Wallet Service routes — `GET /wallets/me`, `/wallets/balances`, `/wallets/transactions` — join this list per `07_Wallet_Service.md`; same pipeline, no special-casing.)*

## Rate Limiting

- Implemented as a Redis token bucket, checked before route resolution.
- Scope for V1: per-client (IP or API key), applied uniformly regardless of route.
- *Open item:* a second, per-user rate limit (post-JWT) may be worth adding later for authenticated endpoints, separate from the pre-auth bucket — not required for V1.

## Failure Responses

| Failure point | Response |
|---|---|
| CORS preflight rejected | `403` |
| Rate limit exceeded | `429` |
| JWT invalid / expired / revoked | `401` |
| Route not found | `404` |
| Downstream service timeout | `504` |
| Downstream service unavailable (circuit open) | `503` |

## Scalability

- Stateless — horizontal scaling behind a load balancer.
- Rate-limit state lives in Redis, shared across all Gateway instances, so limits apply consistently regardless of which instance handles a given request.