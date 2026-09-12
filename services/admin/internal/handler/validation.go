package handler

import (
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidUserID   = errors.New("user_id must be a valid UUID")
	ErrInvalidAsset    = errors.New("asset must be 2-10 uppercase alphanumeric characters (e.g. BTC, INR, USDT)")
	ErrInvalidMarketID = errors.New("market_id must follow the pattern BASE-QUOTE (e.g. BTC-INR)")
	ErrInvalidReason   = errors.New("reason is required and must be between 5 and 500 characters")

	assetRegex  = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	marketRegex = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`)
)

// ValidateReason checks that a reason is not blank and is between 5 and 500 characters after trimming.
func ValidateReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) < 5 || len(trimmed) > 500 {
		return "", ErrInvalidReason
	}
	return trimmed, nil
}

// ValidateUserID asserts that the given user_id is a valid UUID string.
func ValidateUserID(userID string) error {
	if _, err := uuid.Parse(userID); err != nil {
		return ErrInvalidUserID
	}
	return nil
}

// ValidateAsset asserts that the asset code is 2-10 uppercase alphanumeric characters.
func ValidateAsset(asset string) error {
	trimmed := strings.TrimSpace(asset)
	if !assetRegex.MatchString(trimmed) {
		return ErrInvalidAsset
	}
	return nil
}

// ValidateMarketID asserts that the market ID is in BASE-QUOTE format.
func ValidateMarketID(marketID string) error {
	trimmed := strings.TrimSpace(marketID)
	if !marketRegex.MatchString(trimmed) {
		return ErrInvalidMarketID
	}
	return nil
}
