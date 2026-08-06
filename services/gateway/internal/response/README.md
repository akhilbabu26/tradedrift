# `internal/response`

Shared helpers for reading HTTP request bodies and writing consistent HTTP responses across all gateway handlers.

---

## `decode.go`

### `DecodeJSON(r *http.Request, dst any) error`

Decodes a JSON request body into `dst`.

```go
var req LoginRequest
if err := response.DecodeJSON(r, &req); err != nil {
    response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
    return
}
```

**Key behaviour — `DisallowUnknownFields`**

The decoder is configured with `DisallowUnknownFields()`. If the client sends a field that does not exist on `dst`, the decode returns an error instead of silently ignoring it. This prevents:

- Silent data loss from field name typos (e.g. `pasword` instead of `password`)
- Undocumented / experimental fields sneaking into production payloads

---

## `errors.go`

### Types

| Type | JSON field | Purpose |
|---|---|---|
| `ErrorResponse.ErrorCode` | `errorCode` | Machine-readable error identifier (e.g. `"AUTH_INVALID_CREDENTIALS"`) |
| `ErrorResponse.Message` | `message` | Human-readable description |
| `ErrorResponse.Timestamp` | `timestamp` | UTC time of the error in RFC 3339 format |

### `WriteError(w, status, code, message)`

Sets `Content-Type: application/json`, writes the given HTTP status code, then encodes an `ErrorResponse` body.

```go
response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "invalid credentials")
```

Produces:

```json
{
  "errorCode": "AUTH_INVALID_CREDENTIALS",
  "message": "invalid credentials",
  "timestamp": "2026-08-06T15:30:00Z"
}
```

### `WriteJSON(w, status, data)`

Sets `Content-Type: application/json`, writes the given HTTP status code, then encodes any value as a JSON body. Used for all successful responses.

```go
response.WriteJSON(w, http.StatusCreated, RegisterResponse{
    UserID:               res.UserId,
    VerificationRequired: res.VerificationRequired,
})
```

---

## Design notes

- Both `WriteError` and `WriteJSON` always set the `Content-Type` header **before** calling `WriteHeader`. In Go's `net/http`, headers must be written before the status code or they are ignored.
- `ErrorCode` uses SCREAMING_SNAKE_CASE strings (e.g. `NOT_FOUND`, `API_RATE_LIMIT_EXCEEDED`) so clients can match on a stable identifier without parsing the human-readable `message`.
- `Timestamp` is always UTC RFC 3339 — no timezone ambiguity for distributed logs or client-side display.
