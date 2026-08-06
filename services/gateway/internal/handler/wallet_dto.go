package handler

import walletv1 "tradedrift/platform/api/gen/wallet/v1"

// ─────────────────────────────────────────────
// Wallet Response DTOs
// ─────────────────────────────────────────────

type AssetBalanceDTO struct {
	Asset            string `json:"asset"`
	AvailableBalance string `json:"availableBalance"`
	ReservedBalance  string `json:"reservedBalance"`
}

type AssetInfoDTO struct {
	AssetCode    string `json:"assetCode"`
	AssetName    string `json:"assetName"`
	Decimals     int32  `json:"decimals"`
	IsEnabled    bool   `json:"isEnabled"`
	SeedAmount   string `json:"seedAmount"`
	DisplayOrder int32  `json:"displayOrder"`
}

type GetBalancesResponse struct {
	Balances []AssetBalanceDTO `json:"balances"`
}

type GetBalanceResponse struct {
	Balance AssetBalanceDTO `json:"balance"`
}

type GetSupportedAssetsResponse struct {
	Assets []AssetInfoDTO `json:"assets"`
}

// ─────────────────────────────────────────────
// Wallet helper constructors
// ─────────────────────────────────────────────

func assetBalanceDTO(b *walletv1.AssetBalance) AssetBalanceDTO {
	if b == nil {
		return AssetBalanceDTO{}
	}
	return AssetBalanceDTO{
		Asset:            b.Asset,
		AvailableBalance: b.AvailableBalance,
		ReservedBalance:  b.ReservedBalance,
	}
}

func assetInfoDTO(a *walletv1.AssetInfo) AssetInfoDTO {
	if a == nil {
		return AssetInfoDTO{}
	}
	return AssetInfoDTO{
		AssetCode:    a.AssetCode,
		AssetName:    a.AssetName,
		Decimals:     a.Decimals,
		IsEnabled:    a.IsEnabled,
		SeedAmount:   a.SeedAmount,
		DisplayOrder: a.DisplayOrder,
	}
}
