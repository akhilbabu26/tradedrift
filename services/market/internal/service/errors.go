package service

import "errors"

var (
	ErrInvalidMarketID   = errors.New("invalid market id")
	ErrInvalidResolution = errors.New("invalid candle resolution")
	ErrInvalidTimeRange  = errors.New("invalid time range: from must be before to")
	ErrInvalidLimit      = errors.New("invalid limit: must be 0 or between 1 and 500")
	ErrInvalidTradeEvent = errors.New("invalid trade event")
	ErrMarketNotFound    = errors.New("market not found")
)
