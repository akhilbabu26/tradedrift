package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	"tradedrift/services/market/internal/repository"
	"tradedrift/services/market/internal/service"
)

// mapToGRPCError sanitizes domain errors so internal database errors are never leaked to clients.
func mapToGRPCError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, service.ErrMarketNotFound), errors.Is(err, repository.ErrMarketNotFound):
		return status.Error(codes.NotFound, "market not found")
	case errors.Is(err, service.ErrInvalidMarketID):
		return status.Error(codes.InvalidArgument, "invalid market id")
	case errors.Is(err, service.ErrInvalidResolution):
		return status.Error(codes.InvalidArgument, "invalid candle resolution")
	case errors.Is(err, service.ErrInvalidTimeRange):
		return status.Error(codes.InvalidArgument, "invalid time range: from must be before to")
	case errors.Is(err, service.ErrInvalidLimit):
		return status.Error(codes.InvalidArgument, "invalid limit: must be 0 or between 1 and 500")
	case errors.Is(err, service.ErrInvalidTradeEvent):
		return status.Error(codes.InvalidArgument, "invalid trade event payload")
	default:
		// Safe generic error returned to external callers
		return status.Error(codes.Internal, "internal server error")
	}
}

func mapDomainMarketToProto(m *repository.Market) *marketv1.Market {
	if m == nil {
		return nil
	}

	var st marketv1.MarketStatus
	switch m.Status {
	case "ACTIVE":
		st = marketv1.MarketStatus_MARKET_STATUS_ACTIVE
	case "HALTED":
		st = marketv1.MarketStatus_MARKET_STATUS_HALTED
	case "MAINTENANCE":
		st = marketv1.MarketStatus_MARKET_STATUS_MAINTENANCE
	default:
		st = marketv1.MarketStatus_MARKET_STATUS_UNSPECIFIED
	}

	return &marketv1.Market{
		Id:          m.ID,
		BaseAsset:   m.BaseAsset,
		QuoteAsset:  m.QuoteAsset,
		TickSize:    m.TickSize.String(),
		LotSize:     m.LotSize.String(),
		Status:      st,
		MinQuantity: m.MinQuantity.String(),
		CreatedAt:   timestamppb.New(m.CreatedAt),
		UpdatedAt:   timestamppb.New(m.UpdatedAt),
	}
}

func mapDomainTickerToProto(t *repository.Ticker24h) *marketv1.Ticker24H {
	if t == nil {
		return nil
	}
	return &marketv1.Ticker24H{
		MarketId:               t.MarketID,
		LastPrice:              t.LastPrice.String(),
		High_24H:               t.High24h.String(),
		Low_24H:                t.Low24h.String(),
		Volume_24H:             t.Volume24h.String(),
		QuoteVolume_24H:        t.QuoteVolume24h.String(),
		PriceChange_24HPercent: t.PriceChange24hPercent.String(),
	}
}

func mapDomainCandleToProto(c *repository.OHLCCandle) *marketv1.Candle {
	if c == nil {
		return nil
	}

	return &marketv1.Candle{
		StartTime:   timestamppb.New(c.StartTime),
		Open:        c.OpenPrice.String(),
		High:        c.HighPrice.String(),
		Low:         c.LowPrice.String(),
		Close:       c.ClosePrice.String(),
		Volume:      c.Volume.String(),
		QuoteVolume: c.QuoteVolume.String(),
	}
}

func mapProtoResolutionToString(res marketv1.CandleResolution) (string, error) {
	switch res {
	case marketv1.CandleResolution_CANDLE_RESOLUTION_1M:
		return "1m", nil
	case marketv1.CandleResolution_CANDLE_RESOLUTION_5M:
		return "5m", nil
	case marketv1.CandleResolution_CANDLE_RESOLUTION_15M:
		return "15m", nil
	case marketv1.CandleResolution_CANDLE_RESOLUTION_1H:
		return "1h", nil
	case marketv1.CandleResolution_CANDLE_RESOLUTION_1D:
		return "1d", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid or unspecified candle resolution")
	}
}
