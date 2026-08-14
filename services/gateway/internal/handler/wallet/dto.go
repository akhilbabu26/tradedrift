package wallet

import (
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
)

type AssetDTO struct {
	Symbol       string `json:"symbol"`
	Name         string `json:"name"`
	Decimals     int32  `json:"decimals"`
	IsEnabled    bool   `json:"isEnabled"`
	SeedAmount   string `json:"seedAmount"`
	DisplayOrder int32  `json:"displayOrder"`
}

type BalanceDTO struct {
	Asset            string `json:"asset"`
	AvailableBalance string `json:"available_balance"`
	ReservedBalance  string `json:"reserved_balance"`
}

func assetDTO(a *walletv1.AssetInfo) AssetDTO {
	if a == nil {
		return AssetDTO{}
	}
	return AssetDTO{
		Symbol:       a.AssetCode,
		Name:         a.AssetName,
		Decimals:     a.Decimals,
		IsEnabled:    a.IsEnabled,
		SeedAmount:   a.SeedAmount,
		DisplayOrder: a.DisplayOrder,
	}
}

func balanceDTO(b *walletv1.AssetBalance) BalanceDTO {
	if b == nil {
		return BalanceDTO{}
	}
	return BalanceDTO{
		Asset:            b.Asset,
		AvailableBalance: b.AvailableBalance,
		ReservedBalance:  b.ReservedBalance,
	}
}
