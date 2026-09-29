package catalog

import (
	"context"
	"fmt"
)

// Stub is an in-memory Client for local development.
type Stub struct{}

func NewStub() *Stub { return &Stub{} }

var _ Client = (*Stub)(nil)

var stubPasses = map[string]Pass{
	"pass_yatri":   {ID: "pass_yatri", EventID: "ky27", PricePaise: 50000},
	"pass_darbar":  {ID: "pass_darbar", EventID: "ky27", PricePaise: 120000},
	"pass_swarnim": {ID: "pass_swarnim", EventID: "ky27", PricePaise: 250000},
}

func (s *Stub) GetPass(ctx context.Context, passID string) (Pass, error) {
	p, ok := stubPasses[passID]
	if !ok {
		return Pass{}, fmt.Errorf("catalog: pass %q not found", passID)
	}
	return p, nil
}

func (s *Stub) GetCoupon(ctx context.Context, couponID, userID string) (Coupon, error) {
	if couponID == "KASHI100" {
		return Coupon{ID: couponID, DiscountPaise: 10000, AppliesToUser: true, Valid: true}, nil
	}
	return Coupon{}, fmt.Errorf("catalog: coupon %q not found", couponID)
}
