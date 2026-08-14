package common

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type SuccessResponse struct {
	Success bool `json:"success"`
}

// FormatTimestamp safely converts a protobuf Timestamp to RFC3339 string.
func FormatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().Format(time.RFC3339)
}
