package middleware

import (
	"context"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthInterceptor returns a gRPC unary interceptor using the grpc-middleware library.
func AuthInterceptor(validToken string) auth.AuthFunc {
	return func(ctx context.Context) (context.Context, error) {
		token, err := auth.AuthFromMD(ctx, "bearer")
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid authorization header")
		}

		if token != validToken {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Token valid — return context (can add user info to ctx here)
		return ctx, nil
	}
}
