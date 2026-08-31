// Package account defines the identity constants for the MM-001 system account.
// The Market Maker uses two different identity representations:
//
//   - WalletUUID: the UUID stored in the Wallet Service DB (wallets.user_id column)
//   - OrderServiceID: the string used in the Order Service (orders.user_id column)
//
// These are deliberately separate because the two services use different identity types.
package account

import "github.com/google/uuid"

const (
	// OrderServiceID is the string user_id used in the Order Service.
	// All ListOrders, GetOrder queries use this string.
	OrderServiceID = "MM-001"

	// WalletUUIDStr is the deterministic UUID used in the Wallet Service.
	// Wallet Service wallets.user_id is UUID type.
	// This is the fixed UUID for the MM-001 system account.
	WalletUUIDStr = "00000000-0000-0000-0000-000000000001"
)

// WalletUUID is the parsed UUID form of the MM-001 wallet identity.
var WalletUUID = uuid.MustParse(WalletUUIDStr)
