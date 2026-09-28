

-- +goose Up

CREATE TABLE IF NOT EXISTS settled_trades (
    -- Unique identifier of the executed trade from the Matching Engine
    trade_id      UUID PRIMARY KEY,

    -- Participant IDs involved in the trade
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,

    -- Market and currency details (e.g. BTC-USDT, base=BTC, quote=USDT)
    market_id     VARCHAR(32) NOT NULL,
    base_asset    VARCHAR(16) NOT NULL,
    quote_asset   VARCHAR(16) NOT NULL,

    -- Execution pricing and volume
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,

    -- Settlement lifecycle status:
    -- PENDING: trade registered locally, awaiting confirmation from Wallet Service.
    -- SETTLED: wallet balance transfers completed successfully.
    -- FAILED:  unrecoverable error occurred or max retries exceeded (dead-letter state).
    status        VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'SETTLED', 'FAILED')),

    -- Monotonic per-market sequence assigned by the Matching Engine.
    -- Passed to Wallet.SettleTrade to ensure idempotent and strictly ordered ledger execution.
    sequence      BIGINT NOT NULL DEFAULT 0,

    -- Number of settlement attempts executed by the recovery worker
    retry_count   INT NOT NULL DEFAULT 0,

    -- Detailed error message from the last failed settlement attempt (for debugging)
    last_error    TEXT,

    -- Timestamp after which this pending trade is eligible for the next recovery attempt.
    -- Used to implement exponential backoff rather than aggressive tight-loop retrying.
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Timestamp when the trade was matched in the Matching Engine
    executed_at   TIMESTAMPTZ NOT NULL,

    -- Timestamp when THIS settlement service first received and persisted the trade event
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Timestamp when wallet transfer completed and status transitioned to SETTLED
    settled_at    TIMESTAMPTZ
);

-- Support fast audit and user trade history lookups
CREATE INDEX IF NOT EXISTS idx_settled_trades_buyer   ON settled_trades(buyer_id);
CREATE INDEX IF NOT EXISTS idx_settled_trades_seller  ON settled_trades(seller_id);

-- Partial index for the recovery worker:
-- Only indexes rows that are still PENDING, sorted by when they are due to be retried (next_retry_at).
-- Stays tiny and fast even when millions of settled trades exist.
CREATE INDEX IF NOT EXISTS idx_settled_trades_pending ON settled_trades(next_retry_at)
    WHERE status = 'PENDING';

-- +goose Down

DROP TABLE IF EXISTS settled_trades;
