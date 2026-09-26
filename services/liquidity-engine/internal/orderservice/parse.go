package orderservice

import (
	"fmt"
	"strings"

	orderv1 "tradedrift/platform/api/gen/order/v1"
)

// parseMarketFromLevelID extracts the market ID from a level ID.
// Format: "MM-BTC-USDT-ASK-01" -> "BTC-USDT"
func parseMarketFromLevelID(levelID string) string {
	parts := strings.Split(levelID, "-")
	if len(parts) >= 4 && parts[0] == "MM" {
		return parts[1] + "-" + parts[2]
	}
	return ""
}

// parseLevelFromClientOrderID extracts the LevelID and generation from a client_order_id.
// Format: "MM-BTC-USDT-ASK-01-G003" -> ("MM-BTC-USDT-ASK-01", 3, nil)
func parseLevelFromClientOrderID(clientOrderID string) (levelID string, gen int, err error) {
	lastG := -1
	for i := len(clientOrderID) - 1; i >= 2; i-- {
		if clientOrderID[i-1] == '-' && clientOrderID[i] == 'G' {
			lastG = i - 1
			break
		}
	}
	if lastG < 0 {
		return "", 0, fmt.Errorf("no '-G' generation suffix found in %q", clientOrderID)
	}

	levelID = clientOrderID[:lastG]
	genStr := clientOrderID[lastG+2:]

	_, err = fmt.Sscanf(genStr, "%d", &gen)
	if err != nil {
		return "", 0, fmt.Errorf("invalid generation %q in %q: %w", genStr, clientOrderID, err)
	}
	return levelID, gen, nil
}

// protoStatusToString converts an Order Service proto status to string.
func protoStatusToString(s orderv1.OrderStatus) string {
	switch s {
	case orderv1.OrderStatus_ORDER_STATUS_OPEN:
		return "OPEN"
	case orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED:
		return "PARTIALLY_FILLED"
	case orderv1.OrderStatus_ORDER_STATUS_FILLED:
		return "FILLED"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLING:
		return "CANCELLING"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}
