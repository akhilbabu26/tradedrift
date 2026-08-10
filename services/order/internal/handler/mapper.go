package handler

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/service"
)

func mapServiceError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, service.ErrInvalidSide),
		errors.Is(err, service.ErrInvalidType),
		errors.Is(err, service.ErrInvalidMarket),
		errors.Is(err, service.ErrInvalidPrice),
		errors.Is(err, service.ErrInvalidQuantity),
		errors.Is(err, service.ErrInvalidPaginationCursor),
		errors.Is(err, repository.ErrInvalidPaginationCursor):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, service.ErrOrderNotFound),
		errors.Is(err, repository.ErrOrderNotFound):
		return status.Error(codes.NotFound, "order not found")

	case errors.Is(err, service.ErrDuplicateIdempotencyKey),
		errors.Is(err, repository.ErrDuplicateIdempotencyKey):
		return status.Error(codes.AlreadyExists, "idempotency key reused with different request parameters")

	case errors.Is(err, service.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, "insufficient funds for order placement")

	case errors.Is(err, service.ErrOrderNotCancellable),
		errors.Is(err, repository.ErrOrderNotCancellable):
		return status.Error(codes.FailedPrecondition, err.Error())

	default:
		return status.Error(codes.Internal, "failed to process order request")
	}
}

func encodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", createdAt.Format(time.RFC3339Nano), id)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func toProtoOrder(o *repository.Order) *orderv1.Order {
	priceStr := ""
	if o.Price != nil {
		priceStr = *o.Price
	}

	idempotencyStr := ""
	if o.IdempotencyKey != nil {
		idempotencyStr = *o.IdempotencyKey
	}

	return &orderv1.Order{
		Id:                o.ID,
		UserId:            o.UserID,
		MarketId:          o.MarketID,
		Side:              toProtoSideEnum(o.Side),
		OrderType:         toProtoTypeEnum(o.OrderType),
		Price:             priceStr,
		Quantity:          o.Quantity,
		FilledQuantity:    o.FilledQuantity,
		RemainingQuantity: o.RemainingQuantity,
		Status:            toProtoStatusEnum(o.Status),
		IdempotencyKey:    idempotencyStr,
		CreatedAt:         timestamppb.New(o.CreatedAt),
		UpdatedAt:         timestamppb.New(o.UpdatedAt),
	}
}

func parseProtoSide(s orderv1.OrderSide) repository.OrderSide {
	switch s {
	case orderv1.OrderSide_ORDER_SIDE_BUY:
		return repository.SideBuy
	case orderv1.OrderSide_ORDER_SIDE_SELL:
		return repository.SideSell
	default:
		return ""
	}
}

func parseProtoType(t orderv1.OrderType) repository.OrderType {
	switch t {
	case orderv1.OrderType_ORDER_TYPE_LIMIT:
		return repository.TypeLimit
	case orderv1.OrderType_ORDER_TYPE_MARKET:
		return repository.TypeMarket
	default:
		return ""
	}
}

func parseProtoStatus(st orderv1.OrderStatus) repository.OrderStatus {
	switch st {
	case orderv1.OrderStatus_ORDER_STATUS_OPEN:
		return repository.StatusOpen
	case orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED:
		return repository.StatusPartiallyFilled
	case orderv1.OrderStatus_ORDER_STATUS_FILLED:
		return repository.StatusFilled
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLING:
		return repository.StatusCancelling
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return repository.StatusCancelled
	default:
		return ""
	}
}

func toProtoSideEnum(s repository.OrderSide) orderv1.OrderSide {
	switch s {
	case repository.SideBuy:
		return orderv1.OrderSide_ORDER_SIDE_BUY
	case repository.SideSell:
		return orderv1.OrderSide_ORDER_SIDE_SELL
	default:
		return orderv1.OrderSide_ORDER_SIDE_UNSPECIFIED
	}
}

func toProtoTypeEnum(t repository.OrderType) orderv1.OrderType {
	switch t {
	case repository.TypeLimit:
		return orderv1.OrderType_ORDER_TYPE_LIMIT
	case repository.TypeMarket:
		return orderv1.OrderType_ORDER_TYPE_MARKET
	default:
		return orderv1.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

func toProtoStatusEnum(st repository.OrderStatus) orderv1.OrderStatus {
	switch st {
	case repository.StatusOpen:
		return orderv1.OrderStatus_ORDER_STATUS_OPEN
	case repository.StatusPartiallyFilled:
		return orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED
	case repository.StatusFilled:
		return orderv1.OrderStatus_ORDER_STATUS_FILLED
	case repository.StatusCancelling:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLING
	case repository.StatusCancelled:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
