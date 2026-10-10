package repository

import (
	"context"
	"time"
)

type WalletReleaseStatus string

const (
	WalletReleasePending   WalletReleaseStatus = "PENDING"
	WalletReleaseCompleted WalletReleaseStatus = "COMPLETED"
	WalletReleaseFailed    WalletReleaseStatus = "FAILED"
)

type WalletRelease struct {
	ID          string
	OrderID     string
	Status      WalletReleaseStatus
	Attempts    int
	LastError   *string
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

type WalletReleaseRepository interface {
	// ClaimPendingWalletReleases atomically claims up to limit pending releases for processing.
	ClaimPendingWalletReleases(ctx context.Context, limit int) ([]*WalletRelease, error)
	// MarkWalletReleaseCompleted marks the release as COMPLETED by release ID.
	MarkWalletReleaseCompleted(ctx context.Context, id string) error
	// MarkWalletReleaseCompletedByOrderID marks the release as COMPLETED by order ID.
	MarkWalletReleaseCompletedByOrderID(ctx context.Context, orderID string) error
	// RecordWalletReleaseError increments attempts and records error message with exponential backoff delay.
	RecordWalletReleaseError(ctx context.Context, id string, errMsg string) error
	// EnqueueWalletRelease inserts a release record with status PENDING idempotently.
	EnqueueWalletRelease(ctx context.Context, orderID string) error
}
