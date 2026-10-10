package kafka

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tradedrift/services/liquidity-engine/internal/account"
)

func TestProducer_MM_UserIDConsistency(t *testing.T) {
	// CTS-3 / LE-2: Invariant verification
	// MM commands MUST use account.WalletUUIDStr consistently across both CREATE and CANCEL.
	// This ensures that Matching Engine's cancel ownership verification (cancel.UserID == existingOrder.UserID)
	// always succeeds for legitimate MM cancels.

	// 1. Verify account.WalletUUIDStr is a valid UUID
	parsedUUID, err := uuid.Parse(account.WalletUUIDStr)
	require.NoError(t, err, "account.WalletUUIDStr must be a valid UUID string")
	assert.Equal(t, account.WalletUUID, parsedUUID, "account.WalletUUIDStr must match account.WalletUUID")

	// 2. Verify buildCreatePayload serializes UserID == account.WalletUUIDStr
	createBytes, err := buildCreatePayload("order-123", "client-123", "BUY", "50000.00", "1.000")
	require.NoError(t, err)

	var createPayload map[string]interface{}
	err = json.Unmarshal(createBytes, &createPayload)
	require.NoError(t, err)

	assert.Equal(t, account.WalletUUIDStr, createPayload["user_id"], "OrderCreated payload must serialize MM UserID")
	assert.Equal(t, "order-123", createPayload["order_id"])
	assert.Equal(t, "client-123", createPayload["client_order_id"])
	assert.Equal(t, "BUY", createPayload["side"])
	assert.Equal(t, "50000.00", createPayload["price"])
	assert.Equal(t, "1.000", createPayload["quantity"])

	// 3. Verify buildCancelPayload serializes UserID == account.WalletUUIDStr
	cancelBytes, err := buildCancelPayload("order-123")
	require.NoError(t, err)

	var cancelPayload map[string]interface{}
	err = json.Unmarshal(cancelBytes, &cancelPayload)
	require.NoError(t, err)

	assert.Equal(t, account.WalletUUIDStr, cancelPayload["user_id"], "OrderCancelRequested payload must serialize MM UserID")
	assert.Equal(t, "order-123", cancelPayload["order_id"])

	// 4. Invariant: CREATE UserID == CANCEL UserID == account.WalletUUIDStr
	assert.Equal(t, createPayload["user_id"], cancelPayload["user_id"])
}
