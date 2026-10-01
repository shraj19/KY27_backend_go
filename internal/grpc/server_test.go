package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"

	pb "ky27/backend/gen/payment/v1"
	"ky27/backend/internal/middleware"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
)

const bufSize = 1024 * 1024

// mockGateway implements payment.PaymentGateway for testing.
type mockGateway struct {
	createOrderFn func(ctx context.Context, o payment.Order) (payment.CreatedOrder, error)
}

func (m *mockGateway) CreateOrder(ctx context.Context, o payment.Order) (payment.CreatedOrder, error) {
	if m.createOrderFn != nil {
		return m.createOrderFn(ctx, o)
	}
	return payment.CreatedOrder{
		OrderID:          o.ID,
		ProviderOrderID:  "provider-123",
		PaymentSessionID: "session-abc",
		Status:           payment.StatusActive,
	}, nil
}

func (m *mockGateway) VerifyPayment(ctx context.Context, orderID string) (payment.Status, error) {
	return payment.StatusActive, nil
}

func (m *mockGateway) OrderExpiry() time.Duration {
	return 30 * time.Minute
}

func (m *mockGateway) WebhookHeaders() payment.WebhookHeaders {
	return payment.WebhookHeaders{
		Signature: "x-test-signature",
		Timestamp: "x-test-timestamp",
	}
}

func (m *mockGateway) VerifyWebhook(signature string, body []byte, timestamp string) (payment.WebhookEvent, error) {
	return payment.WebhookEvent{
		OrderID:     "test-order",
		Status:      payment.StatusPaid,
		AmountPaise: 10000,
		PaidAt:      time.Now(),
		RawPayload:  body,
	}, nil
}


// mockQueries implements the db methods needed by order.Service.
type mockQueries struct{}

func (m *mockQueries) FindIdempotencyKey(ctx context.Context, key string) (interface{}, error) {
	return nil, fmt.Errorf("not found")
}

func (m *mockQueries) CreateOrder(ctx context.Context, params interface{}) (interface{}, error) {
	return nil, nil
}

func (m *mockQueries) CreateOrderItem(ctx context.Context, params interface{}) (interface{}, error) {
	return nil, nil
}

func (m *mockQueries) InsertIdempotencyKey(ctx context.Context, params interface{}) (interface{}, error) {
	return nil, nil
}

func setupTestServer(t *testing.T, token string) (pb.PaymentServiceClient, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(middleware.AuthInterceptor(token))),
	)

	// Create order service with mock gateway (no real DB for now)
	gateway := &mockGateway{}
	orderSvc := order.NewService(gateway, nil, "mock")

	pb.RegisterPaymentServiceServer(srv, NewPaymentServer(orderSvc))

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exited: %v", err)
		}
	}()

	// Create client
	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}

	cleanup := func() {
		conn.Close()
		srv.Stop()
	}

	return pb.NewPaymentServiceClient(conn), cleanup
}

func TestCreatePayment_AuthRequired(t *testing.T) {
	client, cleanup := setupTestServer(t, "test-token")
	defer cleanup()

	tests := []struct {
		name     string
		token    string
		wantCode codes.Code
	}{
		{
			name:     "no token",
			token:    "",
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "wrong token",
			token:    "wrong-token",
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "valid token but invalid request",
			token:    "test-token",
			wantCode: codes.InvalidArgument, // missing buyer
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "bearer "+tt.token)
			}

			_, err := client.CreatePayment(ctx, &pb.CreatePaymentRequest{})

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %v", err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("expected code %v, got %v: %s", tt.wantCode, st.Code(), st.Message())
			}
		})
	}
}

func TestCreatePayment_Validation(t *testing.T) {
	client, cleanup := setupTestServer(t, "test-token")
	defer cleanup()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "bearer test-token")

	tests := []struct {
		name    string
		req     *pb.CreatePaymentRequest
		wantErr string
	}{
		{
			name:    "missing buyer",
			req:     &pb.CreatePaymentRequest{},
			wantErr: "buyer is required",
		},
		{
			name: "missing buyer id",
			req: &pb.CreatePaymentRequest{
				Buyer: &pb.Buyer{Phone: "123"},
			},
			wantErr: "buyer.id is required",
		},
		{
			name: "missing buyer phone",
			req: &pb.CreatePaymentRequest{
				Buyer: &pb.Buyer{Id: "user1"},
			},
			wantErr: "buyer.phone is required",
		},
		{
			name: "missing items",
			req: &pb.CreatePaymentRequest{
				Buyer: &pb.Buyer{Id: "user1", Phone: "123"},
			},
			wantErr: "items cannot be empty",
		},
		{
			name: "zero total",
			req: &pb.CreatePaymentRequest{
				Buyer:      &pb.Buyer{Id: "user1", Phone: "123"},
				Items:      []*pb.Item{{PassId: "VIP"}},
				TotalPaise: 0,
			},
			wantErr: "total_paise must be positive",
		},
		{
			name: "missing idempotency key",
			req: &pb.CreatePaymentRequest{
				Buyer:      &pb.Buyer{Id: "user1", Phone: "123"},
				Items:      []*pb.Item{{PassId: "VIP"}},
				TotalPaise: 1000,
			},
			wantErr: "idempotency_key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreatePayment(ctx, tt.req)

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %v", err)
			}
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", st.Code())
			}
			if st.Message() != tt.wantErr {
				t.Errorf("expected message %q, got %q", tt.wantErr, st.Message())
			}
		})
	}
}
