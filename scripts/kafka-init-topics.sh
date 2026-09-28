#!/bin/sh
# kafka-init-topics.sh
# Explicitly provisions all Kafka topics required by TradeDrift services.
# Uses --if-not-exists so this script is idempotent: safe to run on every
# docker compose up, even when topics already exist from a previous run.
#
# Topic design decisions:
#   - Business topics: 3 partitions (matches KAFKA_NUM_PARTITIONS default, enables per-market
#     key-based routing in matching-engine, order, settlement, and wallet pipelines)
#   - DLQ topics: 1 partition (single consumer, no ordering benefit from multiple partitions,
#     keeps operational inspection simple)
#   - replication-factor: 1 (single-broker dev environment; increase for production)

set -e

KAFKA_ADDR="${KAFKA_ADDR:-kafka:29092}"
RETRY_LIMIT=30
RETRY_DELAY=2

echo "[kafka-init] Waiting for Kafka broker at ${KAFKA_ADDR}..."
i=0
until /opt/kafka/bin/kafka-topics.sh --bootstrap-server "${KAFKA_ADDR}" --list > /dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge "$RETRY_LIMIT" ]; then
    echo "[kafka-init] ERROR: Kafka not ready after ${RETRY_LIMIT} attempts. Exiting."
    exit 1
  fi
  echo "[kafka-init] Kafka not ready yet (attempt ${i}/${RETRY_LIMIT}), retrying in ${RETRY_DELAY}s..."
  sleep "${RETRY_DELAY}"
done
echo "[kafka-init] Kafka is ready."

create_topic() {
  NAME=$1
  PARTITIONS=$2
  echo "[kafka-init] Creating topic: ${NAME} (partitions=${PARTITIONS})"
  /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${KAFKA_ADDR}" \
    --create \
    --if-not-exists \
    --topic "${NAME}" \
    --partitions "${PARTITIONS}" \
    --replication-factor 1
}

# ── Business Topics (3 partitions) ────────────────────────────────────────────
# orders.commands      : Matching Engine consumes order placement/cancel commands
#                        Partitioned by market (BTC=0, ETH=1, SOL=2)
create_topic "orders.commands"          3

# orders.cancelled.v1  : Order Service publishes cancel confirmations
create_topic "orders.cancelled.v1"      3

# trades.executed      : Matching Engine publishes fill events
#                        Consumed by: Settlement, Market (ticker), Notification, LE
create_topic "trades.executed"          3

# trades.settled.v1    : Wallet Service publishes post-settlement confirmations
#                        Consumed by: Trade Service, Portfolio Service, Order Service
create_topic "trades.settled.v1"        3

# portfolio.user.trades.v1 : Wallet Service publishes per-user trade records
#                            Consumed by: Portfolio Service
create_topic "portfolio.user.trades.v1" 3

# portfolios.updated.v1 : Portfolio Service publishes portfolio change events
#                         Consumed by: Notification Service
create_topic "portfolios.updated.v1"    3

# ── DLQ Topics (1 partition) ──────────────────────────────────────────────────
# trades.settled.dlq   : Poison messages from Portfolio and Trade consumers
#                        Written when processing trades.settled.v1 fails permanently.
#                        Inspect with: kafka-console-consumer --topic trades.settled.dlq
create_topic "trades.settled.dlq"       1

# notifications.dlq    : Poison messages from the Notification consumer
#                        Written when processing portfolios.updated.v1 fails permanently.
create_topic "notifications.dlq"        1

echo "[kafka-init] All topics provisioned successfully."
/opt/kafka/bin/kafka-topics.sh --bootstrap-server "${KAFKA_ADDR}" --list
