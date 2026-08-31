// Package account defines the identity constants for the MM-001 system account.
//
// Identity mapping:
//
//   - WalletUUID / WalletUUIDStr: the deterministic UUID used everywhere a UUID type is required.
//     Used by: Wallet Service (wallets.user_id UUID), Order Service gRPC (ListOrders userId),
//     Matching Engine Kafka commands (OrderCreated.user_id, OrderCancelRequested.user_id).
//
//   - OrderServiceID: the human-readable string label for logging/display only.
//     NOT used as a database or Kafka field — all services validate user_id as a UUID.
package account

import "github.com/google/uuid"

const (
	// OrderServiceID is the human-readable label for the MM-001 account.
	// Use for logging and display only — NOT for DB queries or Kafka messages.
	OrderServiceID = "MM-001"

	// WalletUUIDStr is the canonical UUID for the MM-001 system account.
	// Used in: Wallet Service wallets.user_id, Order Service gRPC ListOrders,
	// and all Kafka command payloads (OrderCreated.user_id, OrderCancelRequested.user_id).
	WalletUUIDStr = "00000000-0000-0000-0000-000000000001"
)

// WalletUUID is the parsed UUID form of the MM-001 identity.
var WalletUUID = uuid.MustParse(WalletUUIDStr)
