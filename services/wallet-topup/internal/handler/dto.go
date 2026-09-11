package handler

import (
	"time"

	"tradedrift/services/wallet-topup/internal/domain"
)

// CreateTopUpRequest models incoming POST /api/v1/topups body.
type CreateTopUpRequest struct {
	INRAmount int64 `json:"inrAmount"`
}

// CreateTopUpResponseDTO matches the camelCase API documentation contract.
type CreateTopUpResponseDTO struct {
	TopUpID         string  `json:"topupId"`
	UserID          string  `json:"userId"`
	INRAmount       int64   `json:"inrAmount"`
	USDTAmount      string  `json:"usdtAmount"`
	ExchangeRate    int64   `json:"exchangeRate"`
	Status          string  `json:"status"`
	Provider        string  `json:"provider"`
	ProviderOrderID *string `json:"providerOrderId,omitempty"`
	ExpiresAt       string  `json:"expiresAt"`
	CreatedAt       string  `json:"createdAt"`
}

func ToTopUpDTO(o *domain.TopUpOrder) *CreateTopUpResponseDTO {
	return &CreateTopUpResponseDTO{
		TopUpID:         o.ID,
		UserID:          o.UserID,
		INRAmount:       o.INRAmount,
		USDTAmount:      o.USDTAmount,
		ExchangeRate:    1000,
		Status:          o.Status,
		Provider:        o.Provider,
		ProviderOrderID: o.ProviderOrderID,
		ExpiresAt:       o.ExpiresAt.Format(time.RFC3339),
		CreatedAt:       o.CreatedAt.Format(time.RFC3339),
	}
}

// DailyUsageResponseDTO matches the camelCase API contract.
type DailyUsageResponseDTO struct {
	UserID       string `json:"userId"`
	UsageDate    string `json:"usageDate"`
	LimitINR     int64  `json:"limitInr"`
	ReservedINR  int64  `json:"reservedInr"`
	ConsumedINR  int64  `json:"consumedInr"`
	RemainingINR int64  `json:"remainingInr"`
	ResetsAt     string `json:"resetsAt"`
}

func ToDailyUsageDTO(u *domain.DailyUsageResponse) *DailyUsageResponseDTO {
	return &DailyUsageResponseDTO{
		UserID:       u.UserID,
		UsageDate:    u.UsageDate,
		LimitINR:     u.LimitINR,
		ReservedINR:  u.ReservedINR,
		ConsumedINR:  u.ConsumedINR,
		RemainingINR: u.RemainingINR,
		ResetsAt:     u.ResetsAt,
	}
}
