package wallet_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/wallet"
)

type mockWalletReleaseRepo struct {
	pendingReleases   []*repository.WalletRelease
	completedIDs      []string
	recordedErrors    map[string]string
	claimErr          error
}

func (m *mockWalletReleaseRepo) ClaimPendingWalletReleases(ctx context.Context, limit int) ([]*repository.WalletRelease, error) {
	if m.claimErr != nil {
		return nil, m.claimErr
	}
	claimed := m.pendingReleases
	m.pendingReleases = nil
	return claimed, nil
}

func (m *mockWalletReleaseRepo) MarkWalletReleaseCompleted(ctx context.Context, id string) error {
	m.completedIDs = append(m.completedIDs, id)
	return nil
}

func (m *mockWalletReleaseRepo) MarkWalletReleaseCompletedByOrderID(ctx context.Context, orderID string) error {
	m.completedIDs = append(m.completedIDs, orderID)
	return nil
}

func (m *mockWalletReleaseRepo) RecordWalletReleaseError(ctx context.Context, id string, errMsg string) error {
	if m.recordedErrors == nil {
		m.recordedErrors = make(map[string]string)
	}
	m.recordedErrors[id] = errMsg
	return nil
}

func (m *mockWalletReleaseRepo) EnqueueWalletRelease(ctx context.Context, orderID string) error {
	m.pendingReleases = append(m.pendingReleases, &repository.WalletRelease{
		ID:      "rel-" + orderID,
		OrderID: orderID,
		Status:  repository.WalletReleasePending,
	})
	return nil
}

type mockFundsReleaser struct {
	releaseErr map[string]error
	calls      []string
}

func (m *mockFundsReleaser) ReleaseFunds(ctx context.Context, orderID string) error {
	m.calls = append(m.calls, orderID)
	if m.releaseErr != nil {
		if err, ok := m.releaseErr[orderID]; ok {
			return err
		}
	}
	return nil
}

func TestWalletReleaseWorker_Success_MarksCompleted(t *testing.T) {
	repo := &mockWalletReleaseRepo{
		pendingReleases: []*repository.WalletRelease{
			{
				ID:      "rel-1",
				OrderID: "ord-1",
				Status:  repository.WalletReleasePending,
			},
		},
	}
	releaser := &mockFundsReleaser{}

	w := wallet.NewWalletReleaseWorker(repo, releaser, zap.NewNop(), 50*time.Millisecond)
	w.ProcessPendingReleases(context.Background())

	require.Len(t, releaser.calls, 1)
	assert.Equal(t, "ord-1", releaser.calls[0])
	require.Len(t, repo.completedIDs, 1)
	assert.Equal(t, "rel-1", repo.completedIDs[0])
	assert.Empty(t, repo.recordedErrors)
}

func TestWalletReleaseWorker_TransientError_RecordsErrorAndRetries(t *testing.T) {
	repo := &mockWalletReleaseRepo{
		pendingReleases: []*repository.WalletRelease{
			{
				ID:       "rel-2",
				OrderID:  "ord-2",
				Status:   repository.WalletReleasePending,
				Attempts: 1,
			},
		},
	}
	releaser := &mockFundsReleaser{
		releaseErr: map[string]error{
			"ord-2": errors.New("wallet service connection refused"),
		},
	}

	w := wallet.NewWalletReleaseWorker(repo, releaser, zap.NewNop(), 50*time.Millisecond)
	w.ProcessPendingReleases(context.Background())

	require.Len(t, releaser.calls, 1)
	assert.Equal(t, "ord-2", releaser.calls[0])
	assert.Empty(t, repo.completedIDs)
	require.Contains(t, repo.recordedErrors, "rel-2")
	assert.Contains(t, repo.recordedErrors["rel-2"], "connection refused")
}

func TestWalletReleaseWorker_ReservationNotFound_TreatedAsCompleted(t *testing.T) {
	repo := &mockWalletReleaseRepo{
		pendingReleases: []*repository.WalletRelease{
			{
				ID:      "rel-3",
				OrderID: "ord-3",
				Status:  repository.WalletReleasePending,
			},
		},
	}
	releaser := &mockFundsReleaser{
		releaseErr: map[string]error{
			"ord-3": errors.New("rpc error: code = NotFound desc = reservation not found"),
		},
	}

	w := wallet.NewWalletReleaseWorker(repo, releaser, zap.NewNop(), 50*time.Millisecond)
	w.ProcessPendingReleases(context.Background())

	require.Len(t, releaser.calls, 1)
	assert.Equal(t, "ord-3", releaser.calls[0])
	require.Len(t, repo.completedIDs, 1)
	assert.Equal(t, "rel-3", repo.completedIDs[0])
	assert.Empty(t, repo.recordedErrors)
}

func TestWalletReleaseWorker_NonReservationNotFound_RecordedAsErrorAndRetried(t *testing.T) {
	repo := &mockWalletReleaseRepo{
		pendingReleases: []*repository.WalletRelease{
			{
				ID:      "rel-4",
				OrderID: "ord-4",
				Status:  repository.WalletReleasePending,
			},
		},
	}
	// "user not found" or "account not found" must NOT be treated as reservation released!
	releaser := &mockFundsReleaser{
		releaseErr: map[string]error{
			"ord-4": errors.New("rpc error: code = NotFound desc = user account not found"),
		},
	}

	w := wallet.NewWalletReleaseWorker(repo, releaser, zap.NewNop(), 50*time.Millisecond)
	w.ProcessPendingReleases(context.Background())

	require.Len(t, releaser.calls, 1)
	assert.Equal(t, "ord-4", releaser.calls[0])
	assert.Empty(t, repo.completedIDs, "generic not found error must NOT be marked completed")
	require.Contains(t, repo.recordedErrors, "rel-4", "generic not found error must be recorded for retry")
}

func TestIsReservationAlreadyReleased_StrictMatching(t *testing.T) {
	// True positives: explicit reservation errors
	assert.True(t, wallet.IsReservationAlreadyReleased(errors.New("reservation not found")))
	assert.True(t, wallet.IsReservationAlreadyReleased(errors.New("funds already released")))
	assert.True(t, wallet.IsReservationAlreadyReleased(errors.New("reservation does not exist")))
	assert.True(t, wallet.IsReservationAlreadyReleased(errors.New("trade already settled")))
	assert.True(t, wallet.IsReservationAlreadyReleased(errors.New("hold already completed")))

	// False positives prevented: generic not found errors must be FALSE
	assert.False(t, wallet.IsReservationAlreadyReleased(errors.New("user not found")))
	assert.False(t, wallet.IsReservationAlreadyReleased(errors.New("account not found")))
	assert.False(t, wallet.IsReservationAlreadyReleased(errors.New("order not found")))
	assert.False(t, wallet.IsReservationAlreadyReleased(errors.New("database record not found")))
	assert.False(t, wallet.IsReservationAlreadyReleased(nil))
}
