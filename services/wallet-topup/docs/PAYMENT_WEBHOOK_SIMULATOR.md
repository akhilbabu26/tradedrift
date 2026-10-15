# Payment Webhook Staging Simulator (PAY-01)

## Overview

The Payment Webhook Staging Simulator (`scripts/simulate_payment_webhook.go`) and test suite (`services/wallet-topup/test/simulator_test.go`) provide end-to-end verification and staging simulation for payment webhooks, specifically testing:

1. **Cryptographic Signature Verification:** Validates standard HMAC-SHA256 signature matching Razorpay webhook payloads (`X-Razorpay-Signature`).
2. **Event Processing:** Supports both `payment.captured` and `order.paid` event types.
3. **Credit Conversion:** Verifies the conversion rate of ₹1 INR = 1,000 virtual USDT.
4. **Idempotency Guarantee:** Validates that duplicate deliveries of identical webhook payloads produce `HTTP 200 OK` without duplicate ledger postings or balance inflation.
5. **Security Hardening:** Ensures tampered signatures and expired replay timestamps are rejected with `HTTP 401 Unauthorized`.

---

## Running Automated Simulator Unit Tests

Run the simulator test suite within the `wallet-topup` microservice:

```bash
cd services/wallet-topup
go test -v ./test/simulator_test.go ./test/mocks.go
```

Or run all wallet-topup tests:

```bash
cd services/wallet-topup
go test ./...
```

---

## Running the Standalone CLI Simulator

The standalone CLI simulator can be executed against a running local instance or staging environment:

```bash
go run ./scripts/simulate_payment_webhook.go [flags]
```

### Supported CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-url` | `http://localhost:8085/api/v1/webhooks/payment/razorpay` | Target webhook URL |
| `-secret` | `staging_rzp_webhook_secret_key` | HMAC-SHA256 signing secret |
| `-order` | *(auto-generated)* | Order or Provider Order ID |
| `-amount` | `5` | Top-up amount in INR (reconciles to `amount * 1000` USDT) |
| `-event` | `payment.captured` | Webhook event: `payment.captured` or `order.paid` |
| `-test-idempotency` | `true` | Executes duplicate replay to ensure idempotent `HTTP 200` |
| `-test-tamper` | `true` | Sends tampered signature to ensure `HTTP 401` rejection |

### Example CLI Output

```text
================================================================
       TradeDrift Payment Webhook Staging Simulator (PAY-01)    
================================================================
Endpoint URL:        http://localhost:8085/api/v1/webhooks/payment/razorpay
Event Type:          payment.captured
Order ID:            order_sim_1792058400000000000
Amount:              ₹5 INR (Reconciles to 5000 USDT)
Signing Secret:      stagin***
----------------------------------------------------------------
[STEP 1] Dispatching primary webhook event... SUCCESS (HTTP 200)
         Response: {"status":"SUCCESS"}
[STEP 2] Dispatching duplicate replay (idempotency check)... SUCCESS (HTTP 200 - idempotent no-op confirmed)
         Response: {"status":"SUCCESS"}
[STEP 3] Dispatching tampered signature (security check)... SUCCESS (HTTP 401 Unauthorized correctly rejected)
         Response: {"error":"invalid signature"}
----------------------------------------------------------------
RESULT: ALL SIMULATOR INTEGRATION CHECKS PASSED SUCCESSFULLY.
================================================================
```
