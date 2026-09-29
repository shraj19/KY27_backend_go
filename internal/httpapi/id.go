package httpapi

import "github.com/google/uuid"

// newOrderID generates a unique, server-side order id. Cashfree requires
// alphanumeric + '_' + '-' only, which a UUID satisfies.
func newOrderID() string {
	return uuid.NewString()
}
