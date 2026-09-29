package httpapi

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"ky27/backend/internal/payment"
)

// Handler serves the payment HTTP API.
type Handler struct {
	gateway  payment.PaymentGateway
	validate *validator.Validate
}

func NewHandler(gateway payment.PaymentGateway) *Handler {
	return &Handler{
		gateway:  gateway,
		validate: validator.New(),
	}
}

// Register mounts routes onto app.
func (h *Handler) Register(app *fiber.App) {
	app.Post("/orders", h.createOrder)
}

// createOrderRequest is the create-order request body.
type createOrderRequest struct {
	AmountPaise int64  `json:"amount_paise" validate:"required,gt=0"`
	Currency    string `json:"currency"      validate:"omitempty,len=3"`
	CustomerID  string `json:"customer_id"   validate:"required"`
	Phone       string `json:"phone"         validate:"required,e164|numeric"`
	Name        string `json:"name"          validate:"required"`
	Email       string `json:"email"         validate:"required,email"`
}

type createOrderResponse struct {
	OrderID          string `json:"order_id"`
	PaymentSessionID string `json:"payment_session_id"`
	Status           string `json:"status"`
}

func (h *Handler) createOrder(c *fiber.Ctx) error {
	var req createOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := h.validate.Struct(req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Order ID is generated server-side.
	order := payment.Order{
		ID:          "KY27-" + newOrderID(),
		AmountPaise: req.AmountPaise,
		Currency:    req.Currency,
		Customer: payment.Customer{
			ID:    req.CustomerID,
			Phone: req.Phone,
			Name:  req.Name,
			Email: req.Email,
		},
	}

	created, err := h.gateway.CreateOrder(c.Context(), order)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "failed to create order: "+err.Error())
	}

	return c.Status(fiber.StatusCreated).JSON(createOrderResponse{
		OrderID:          created.OrderID,
		PaymentSessionID: created.PaymentSessionID,
		Status:           string(created.Status),
	})
}
