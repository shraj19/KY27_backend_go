// Package catalog provides access to catalog data (passes, coupons) owned by
// the catalog service. Implementations may be a local stub or a gRPC client.
package catalog

import "context"

// Pass is catalog data about a pass.
type Pass struct {
	ID         string
	EventID    string
	PricePaise int64
}

// Coupon is catalog data for applying a coupon to a user.
type Coupon struct {
	ID            string
	DiscountPaise int64
	AppliesToUser bool
	Valid         bool
}

// Client provides catalog lookups.
type Client interface {
	GetPass(ctx context.Context, passID string) (Pass, error)
	GetCoupon(ctx context.Context, couponID, userID string) (Coupon, error)
}
