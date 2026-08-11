package service

import "errors"

var (
	ErrInvalidMarketID   = errors.New("invalid market id")
	ErrInvalidResolution = errors.New("invalid candle resolution")
	ErrInvalidTimeRange  = errors.New("invalid time range: from must be before to")
	ErrInvalidLimit      = errors.New("invalid limit: must be between 1 and 500")
	ErrMarketNotFound    = errors.New("market not found")
)
