package test

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
)

type mockRawOrderServiceClient struct {
	createResp  *orderv1.CreateOrderResponse
	createErr   error
	getResp     *orderv1.GetOrderResponse
	getErr      error
	cancelResp  *orderv1.CancelOrderResponse
	cancelErr   error
	listResp    *orderv1.ListOrdersResponse
	listErr     error
	listHandler func(req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error)
}

func (m *mockRawOrderServiceClient) CreateOrder(ctx context.Context, in *orderv1.CreateOrderRequest, opts ...grpc.CallOption) (*orderv1.CreateOrderResponse, error) {
	return m.createResp, m.createErr
}

func (m *mockRawOrderServiceClient) CancelOrder(ctx context.Context, in *orderv1.CancelOrderRequest, opts ...grpc.CallOption) (*orderv1.CancelOrderResponse, error) {
	return m.cancelResp, m.cancelErr
}

func (m *mockRawOrderServiceClient) GetOrder(ctx context.Context, in *orderv1.GetOrderRequest, opts ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	return m.getResp, m.getErr
}

func (m *mockRawOrderServiceClient) ListOrders(ctx context.Context, in *orderv1.ListOrdersRequest, opts ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	if m.listHandler != nil {
		return m.listHandler(in)
	}
	return m.listResp, m.listErr
}

func (m *mockRawOrderServiceClient) CancelAllOrders(ctx context.Context, in *orderv1.CancelAllOrdersRequest, opts ...grpc.CallOption) (*orderv1.CancelAllOrdersResponse, error) {
	return nil, nil
}

func TestClient_NilResponseProtection(t *testing.T) {
	logger := zap.NewNop()

	// 1. CreateOrder returns nil response
	mockClient := &mockRawOrderServiceClient{
		createResp: nil,
		createErr:  nil,
	}
	client := orderservice.NewClientWithServiceClient(mockClient, logger)

	_, err := client.CreateCrossingOrder(context.Background(), "BTC-USDT", "BUY", "96500", "0.01", "key-1")
	if err == nil {
		t.Fatalf("expected error on nil CreateOrder response, got nil")
	}

	// 1b. CreateOrder returns response with nil Order
	mockClient.createResp = &orderv1.CreateOrderResponse{Order: nil}
	_, err = client.CreateCrossingOrder(context.Background(), "BTC-USDT", "BUY", "96500", "0.01", "key-1")
	if err == nil {
		t.Fatalf("expected error on nil Order inside CreateOrderResponse, got nil")
	}

	// 2. GetOrder returns nil response
	mockClient.getResp = nil
	_, err = client.GetOrder(context.Background(), "ord-1")
	if err == nil {
		t.Fatalf("expected error on nil GetOrder response, got nil")
	}

	// 2b. GetOrder returns response with nil Order
	mockClient.getResp = &orderv1.GetOrderResponse{Order: nil}
	_, err = client.GetOrder(context.Background(), "ord-1")
	if err == nil {
		t.Fatalf("expected error on nil Order inside GetOrderResponse, got nil")
	}

	// 3. CancelOrder returns nil response
	mockClient.cancelResp = nil
	_, err = client.CancelOrder(context.Background(), "ord-1")
	if err == nil {
		t.Fatalf("expected error on nil CancelOrder response, got nil")
	}

	// 3b. CancelOrder returns response with nil Order
	mockClient.cancelResp = &orderv1.CancelOrderResponse{Order: nil}
	_, err = client.CancelOrder(context.Background(), "ord-1")
	if err == nil {
		t.Fatalf("expected error on nil Order inside CancelOrderResponse, got nil")
	}
}

func TestClient_Ping(t *testing.T) {
	logger := zap.NewNop()

	// 1. Success case: ListOrders returns successfully
	mockClient := &mockRawOrderServiceClient{
		listResp: &orderv1.ListOrdersResponse{Orders: []*orderv1.Order{}},
		listErr:  nil,
	}
	client := orderservice.NewClientWithServiceClient(mockClient, logger)

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("expected healthy ping, got: %v", err)
	}

	// 2. Failure case: ListOrders returns error (e.g. database down)
	mockClient.listErr = context.DeadlineExceeded
	if err := client.Ping(context.Background()); err == nil {
		t.Fatalf("expected ping error when ListOrders fails, got nil")
	}

	// 3. Uninitialized client case
	uninitClient := &orderservice.Client{}
	if err := uninitClient.Ping(context.Background()); err == nil {
		t.Fatalf("expected ping error when client is uninitialized, got nil")
	}
}

func TestClient_FindOrderByIdempotencyKey_Pagination(t *testing.T) {
	logger := zap.NewNop()

	// 1. Found on Page 1
	mockClient := &mockRawOrderServiceClient{
		listResp: &orderv1.ListOrdersResponse{
			Orders: []*orderv1.Order{
				{
					Id:                "ord-101",
					IdempotencyKey:    "key-101",
					Status:            orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED,
					FilledQuantity:    "0.0200",
					RemainingQuantity: "0.0300",
				},
			},
		},
	}
	client := orderservice.NewClientWithServiceClient(mockClient, logger)

	res, err := client.FindOrderByIdempotencyKey(context.Background(), "BTC-USDT", "key-101")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.OrderID != "ord-101" || res.FilledQty != "0.0200" || res.RemainingQty != "0.0300" {
		t.Fatalf("unexpected result on page 1 match: %+v", res)
	}

	// 2. Found on Page 2 via cursor
	page1ReqReceived := false
	page2ReqReceived := false
	mockClient = &mockRawOrderServiceClient{
		listHandler: func(req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
			if req.Cursor == "" {
				page1ReqReceived = true
				// Return 50 orders of filler with next cursor
				orders := make([]*orderv1.Order, 50)
				for i := 0; i < 50; i++ {
					orders[i] = &orderv1.Order{Id: "other", IdempotencyKey: "other-key"}
				}
				return &orderv1.ListOrdersResponse{
					Orders:     orders,
					NextCursor: "cursor-page-2",
				}, nil
			} else if req.Cursor == "cursor-page-2" {
				page2ReqReceived = true
				return &orderv1.ListOrdersResponse{
					Orders: []*orderv1.Order{
						{
							Id:                "ord-page2",
							IdempotencyKey:    "target-key-p2",
							Status:            orderv1.OrderStatus_ORDER_STATUS_FILLED,
							FilledQuantity:    "0.0500",
							RemainingQuantity: "0",
						},
					},
					NextCursor: "",
				}, nil
			}
			return &orderv1.ListOrdersResponse{}, nil
		},
	}
	client = orderservice.NewClientWithServiceClient(mockClient, logger)

	res, err = client.FindOrderByIdempotencyKey(context.Background(), "BTC-USDT", "target-key-p2")
	if err != nil {
		t.Fatalf("unexpected error on page 2: %v", err)
	}
	if !page1ReqReceived || !page2ReqReceived {
		t.Fatalf("expected both page 1 and page 2 requests, got p1=%v, p2=%v", page1ReqReceived, page2ReqReceived)
	}
	if res == nil || res.OrderID != "ord-page2" {
		t.Fatalf("expected ord-page2, got %+v", res)
	}

	// 3. Exhausted search returns (nil, nil)
	res, err = client.FindOrderByIdempotencyKey(context.Background(), "BTC-USDT", "non-existent-key")
	if err != nil {
		t.Fatalf("expected nil error on absent order, got: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on absent order, got: %+v", res)
	}
}
