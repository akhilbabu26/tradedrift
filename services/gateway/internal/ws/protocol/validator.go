package protocol

import (
	"encoding/json"
	"strings"
)

// ParseStreamType extracts the stream type and target from a stream path.
func ParseStreamType(stream string) (streamType, target string) {
	streamType, target, _ = parseStreamStrict(stream)
	return
}

// ValidateStream returns (streamType, target, ok=true) only for fully valid stream names.
// Strict stream validation rejects any pattern not matching:
// - "market:orderbook:{id}"
// - "market:ticker:{id}"
// - "market:trades:{id}"
// - "user:notifications:{user_id}"
func ValidateStream(stream string) (streamType, target string, ok bool) {
	return parseStreamStrict(stream)
}

func parseStreamStrict(stream string) (streamType, target string, ok bool) {
	parts := strings.SplitN(stream, ":", 3)
	if len(parts) != 3 {
		return StreamTypeControl, "", false
	}

	prefix, sub, id := parts[0], parts[1], parts[2]

	// Reject empty target IDs
	if id == "" {
		return StreamTypeControl, "", false
	}

	switch prefix {
	case "market":
		switch sub {
		case "orderbook":
			return StreamTypeOrderBook, id, true
		case "ticker":
			return StreamTypeTicker, id, true
		case "trades":
			return StreamTypeTrades, id, true
		}
	case "user":
		if sub == "notifications" {
			return StreamTypeNotification, id, true
		}
	}

	return StreamTypeControl, "", false
}

// MarshalEnvelope marshals an OutboundEnvelope into JSON bytes.
func MarshalEnvelope(env OutboundEnvelope) []byte {
	b, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return b
}
