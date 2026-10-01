package middleware

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthInterceptor(t *testing.T) {
	authFunc := AuthInterceptor("valid-token")

	tests := []struct {
		name      string
		ctx       context.Context
		wantCode  codes.Code
		wantError bool
	}{
		{
			name:      "missing metadata",
			ctx:       context.Background(),
			wantCode:  codes.Unauthenticated,
			wantError: true,
		},
		{
			name: "missing authorization header",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs("other-header", "value"),
			),
			wantCode:  codes.Unauthenticated,
			wantError: true,
		},
		{
			name: "invalid token",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs("authorization", "bearer wrong-token"),
			),
			wantCode:  codes.Unauthenticated,
			wantError: true,
		},
		{
			name: "malformed authorization header",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs("authorization", "not-bearer-format"),
			),
			wantCode:  codes.Unauthenticated,
			wantError: true,
		},
		{
			name: "valid token",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs("authorization", "bearer valid-token"),
			),
			wantCode:  codes.OK,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := authFunc(tt.ctx)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantCode {
					t.Errorf("expected code %v, got %v", tt.wantCode, st.Code())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}
