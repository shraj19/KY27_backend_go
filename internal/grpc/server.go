package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "ky27/backend/gen/payment/v1"
	"ky27/backend/internal/order"
	"ky27/backend/internal/payment"
)

// PaymentServer implements the PaymentService gRPC interface.
type PaymentServer struct {
	pb.UnimplementedPaymentServiceServer
	orderSvc *order.Service
}

// NewPaymentServer creates a new gRPC payment server.
func NewPaymentServer(orderSvc *order.Service) *PaymentServer {
	return &PaymentServer{orderSvc: orderSvc}
}

// -----------------------------------------------------------------------------
// CreatePayment
// -----------------------------------------------------------------------------

func (s *PaymentServer) CreatePayment(ctx context.Context, req *pb.CreatePaymentRequest) (*pb.CreatePaymentResponse, error) {
	if err := validateCreateRequest(req); err != nil {
		return nil, err
	}

	result, err := s.orderSvc.Create(ctx, order.Request{
		OrderID:        req.OrderId,
		Buyer:          toBuyer(req.Buyer),
		Items:          toItems(req.Items),
		TotalPaise:     req.TotalPaise,
		Currency:       req.Currency,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &pb.CreatePaymentResponse{
		OrderId:          result.OrderID,
		PaymentSessionId: result.PaymentSessionID,
		Status:           toProtoStatus(result.Status),
	}, nil
}

// -----------------------------------------------------------------------------
// GetPaymentStatus
// -----------------------------------------------------------------------------

func (s *PaymentServer) GetPaymentStatus(ctx context.Context, req *pb.GetPaymentStatusRequest) (*pb.GetPaymentStatusResponse, error) {
	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	st, err := s.orderSvc.GetStatus(ctx, req.OrderId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "order not found")
	}

	return &pb.GetPaymentStatusResponse{
		OrderId: req.OrderId,
		Status:  toProtoStatus(st),
	}, nil
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

func validateCreateRequest(req *pb.CreatePaymentRequest) error {
	if req.Buyer == nil {
		return status.Error(codes.InvalidArgument, "buyer is required")
	}
	if req.Buyer.Id == "" {
		return status.Error(codes.InvalidArgument, "buyer.id is required")
	}
	if req.Buyer.Phone == "" {
		return status.Error(codes.InvalidArgument, "buyer.phone is required")
	}
	if len(req.Items) == 0 {
		return status.Error(codes.InvalidArgument, "items cannot be empty")
	}
	if req.TotalPaise <= 0 {
		return status.Error(codes.InvalidArgument, "total_paise must be positive")
	}
	if req.IdempotencyKey == "" {
		return status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Mappers
// -----------------------------------------------------------------------------

func toBuyer(b *pb.Buyer) order.Buyer {
	if b == nil {
		return order.Buyer{}
	}
	return order.Buyer{
		ID:    b.Id,
		Phone: b.Phone,
		Email: b.Email,
		Name:  b.Name,
	}
}

func toItems(items []*pb.Item) []order.Item {
	result := make([]order.Item, len(items))
	for i, item := range items {
		result[i] = order.Item{
			PassID:        item.PassId,
			AmountPaise:   item.AmountPaise,
			AttendeeName:  item.AttendeeName,
			AttendeeEmail: item.AttendeeEmail,
			AttendeePhone: item.AttendeePhone,
		}
	}
	return result
}

func toProtoStatus(s payment.Status) pb.PaymentStatus {
	switch s {
	case payment.StatusActive:
		return pb.PaymentStatus_PAYMENT_STATUS_ACTIVE
	case payment.StatusPaid:
		return pb.PaymentStatus_PAYMENT_STATUS_PAID
	case payment.StatusExpired:
		return pb.PaymentStatus_PAYMENT_STATUS_EXPIRED
	case payment.StatusFailed:
		return pb.PaymentStatus_PAYMENT_STATUS_FAILED
	default:
		return pb.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}

func mapError(err error) error {
	switch err {
	case order.ErrIdempotencyMismatch:
		return status.Error(codes.AlreadyExists, "idempotency key reused with different request body")
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
