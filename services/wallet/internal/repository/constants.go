package repository

// Reservation statuses — used in wallet_reservations.status column.
const (
	ReservationActive            = "ACTIVE"
	ReservationPartiallyConsumed = "PARTIALLY_CONSUMED"
	ReservationConsumed          = "CONSUMED"
	ReservationReleased          = "RELEASED"
)

// Transaction types — used in wallet_transactions.transaction_type column.
const (
	TxnTypeCredit = "CREDIT"
	TxnTypeDebit  = "DEBIT"
)

// Reference types — used in wallet_transactions.reference_type column.
// Identifies what caused the balance change.
const (
	RefInitialAllocation = "INITIAL_ALLOCATION" // Seeded on registration
	RefReservation       = "RESERVATION"        // Funds locked for an order
	RefRelease           = "RELEASE"            // Locked funds returned on cancel
	RefSettlement        = "SETTLEMENT"         // Trade settled (buyer credited, seller debited)
	RefDeposit           = "DEPOSIT"            // External deposit
	RefWithdrawal        = "WITHDRAWAL"         // External withdrawal
)
