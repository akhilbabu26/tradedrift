// Package account defines the identity constants for the CT-001 system account.
//
// Identity mapping:
//   - WalletUUID / WalletUUIDStr: Canonical UUID used by Wallet Service and Order Service.
//   - TakerServiceID: Human-readable string label for logging and metrics.
package account

import "github.com/google/uuid"

const (
	// TakerServiceID is the human-readable label for the CT-001 account.
	TakerServiceID = "CT-001"

	// WalletUUIDStr is the canonical UUID for the CT-001 system taker account.
	// Used in: Wallet Service wallets.user_id, Order Service CreateOrder.user_id.
	WalletUUIDStr = "00000000-0000-0000-0000-000000000002"
)

// WalletUUID is the parsed UUID form of the CT-001 identity.
var WalletUUID = uuid.MustParse(WalletUUIDStr)
