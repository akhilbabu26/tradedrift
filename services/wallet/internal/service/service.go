package service

import (
	"go.uber.org/zap"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet/internal/repository"
	postgresRepo "tradedrift/services/wallet/internal/repository/postgres"
)

// Service holds all dependencies for wallet business logic.
type Service struct {
	db               *pgxpool.Pool
	walletRepo       repository.WalletRepository
	reservRepo       repository.ReservationRepository
	txnRepo          repository.TransactionRepository
	assetRepo        repository.AssetRepository
	outboxRepo       repository.OutboxRepository
	settledTradeRepo repository.SettledTradeRepository
	log              *zap.Logger
}

// NewService creates a new Service instance with all dependencies wired.
func NewService(db *pgxpool.Pool, log *zap.Logger) *Service {
	return &Service{
		db:               db,
		walletRepo:       postgresRepo.NewWalletRepository(db),
		reservRepo:       postgresRepo.NewReservationRepository(db),
		txnRepo:          postgresRepo.NewTransactionRepository(db),
		assetRepo:        postgresRepo.NewAssetRepository(db),
		outboxRepo:       postgresRepo.NewOutboxRepository(db),
		settledTradeRepo: postgresRepo.NewSettledTradeRepository(db),
		log:              log,
	}
}

