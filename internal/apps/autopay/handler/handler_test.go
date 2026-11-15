package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubService struct {
	createErr  error
	webhookErr error
	gotGateway string
	gotConfig  uuid.UUID
	gotBody    []byte
}

func (s *stubService) Create(_ context.Context, req service.CreateRequest) (*service.CreateResult, error) {
	s.gotGateway = req.Gateway
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &service.CreateResult{AutopayMandateID: uuid.New(), Gateway: req.Gateway, PaymentType: req.PaymentType, Amount: 100, Currency: "INR", Mode: "sandbox"}, nil
}
func (s *stubService) Verify(context.Context, uuid.UUID) (*service.MandateView, error) {
	return &service.MandateView{}, nil
}
func (s *stubService) Cancel(context.Context, uuid.UUID) (*service.MandateView, error) {
	return nil, service.ErrNotFound
}
func (s *stubService) Get(uuid.UUID) (*service.MandateView, error) {
	return &service.MandateView{}, nil
}
func (s *stubService) Status(uuid.UUID, string) (*service.StatusView, error) {
	return &service.StatusView{ActiveSubscription: true}, nil
}
func (s *stubService) AvailableGateways(string) ([]string, error) { return nil, nil }
func (s *stubService) HandleWebhook(_ context.Context, cfg uuid.UUID, _ http.Header, body []byte) error {
	s.gotConfig, s.gotBody = cfg, body
	return s.webhookErr
}

func newRouter(svc *stubService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api/v1"), New(svc))
	return r
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRoutesRegisterWithoutConflictAndResolve(t *testing.T) {
	r := newRouter(&stubService{})
	id := uuid.New().String()
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/v1/autopay/status?user_id=" + uuid.New().String() + "&app_name=app", 200},
		{"GET", "/api/v1/autopay/gateways?app_name=app", 200},
		{"GET", "/api/v1/autopay/" + id, 200},
		{"POST", "/api/v1/autopay/" + id + "/verify", 200},
		{"POST", "/api/v1/autopay/" + id + "/cancel", 404},
		{"GET", "/api/v1/autopay/not-a-uuid", 400},
		{"GET", "/api/v1/autopay/status", 400},
	} {
		if w := do(r, tc.method, tc.path, ""); w.Code != tc.want {
			t.Errorf("%s %s = %d, want %d (%s)", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestCreateValidationAndStatusMapping(t *testing.T) {
	valid := `{"user_id":"` + uuid.New().String() + `","app_name":"app","gateway":"razorpay","payment_type":"recurring",
	"authorization_amount":100,"max_debit_amount":29900,"start_at":"` + time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339) + `","billing_frequency":"monthly"}`

	svc := &stubService{}
	r := newRouter(svc)
	if w := do(r, "POST", "/api/v1/autopay", valid); w.Code != http.StatusCreated || svc.gotGateway != "razorpay" {
		t.Fatalf("valid create = %d gateway=%q body=%s", w.Code, svc.gotGateway, w.Body.String())
	}
	for name, body := range map[string]string{
		"missing gateway":  `{"user_id":"` + uuid.New().String() + `","app_name":"app","payment_type":"recurring","start_at":"2030-01-01T00:00:00Z","billing_frequency":"monthly"}`,
		"as_presented":     `{"user_id":"` + uuid.New().String() + `","app_name":"app","gateway":"x","payment_type":"recurring","start_at":"2030-01-01T00:00:00Z","billing_frequency":"as_presented"}`,
		"bad payment type": `{"user_id":"` + uuid.New().String() + `","app_name":"app","gateway":"x","payment_type":"weekly","start_at":"2030-01-01T00:00:00Z","billing_frequency":"monthly"}`,
		"not json":         `nope`,
	} {
		if w := do(r, "POST", "/api/v1/autopay", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, w.Code)
		}
	}

	for err, want := range map[error]int{
		service.ErrInvalidRequest:      http.StatusBadRequest,
		service.ErrGatewayNotAvailable: http.StatusConflict,
		service.ErrGatewayUnavailable:  http.StatusServiceUnavailable,
		service.ErrNotFound:            http.StatusNotFound,
		errors.New("boom"):             http.StatusInternalServerError,
	} {
		r := newRouter(&stubService{createErr: err})
		if w := do(r, "POST", "/api/v1/autopay", valid); w.Code != want {
			t.Errorf("service error %v mapped to %d, want %d", err, w.Code, want)
		}
	}
}

func TestWebhookRoutePassesRawBodyAndConfig(t *testing.T) {
	cfg := uuid.New()
	svc := &stubService{}
	r := newRouter(svc)
	body := `{"event":"payment.captured"}`
	if w := do(r, "POST", "/api/v1/autopay/webhooks/"+cfg.String(), body); w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	if svc.gotConfig != cfg || string(svc.gotBody) != body {
		t.Fatalf("got config=%s body=%q", svc.gotConfig, svc.gotBody)
	}
	if w := do(r, "POST", "/api/v1/autopay/webhooks/not-a-uuid", body); w.Code != 404 {
		t.Fatalf("a malformed config id must be 404, got %d", w.Code)
	}
	for err, want := range map[error]int{
		service.ErrInvalidWebhookSignature: http.StatusUnauthorized,
		service.ErrNotFound:                http.StatusNotFound,
		errors.New("db down"):              http.StatusInternalServerError,
	} {
		r := newRouter(&stubService{webhookErr: err})
		if w := do(r, "POST", "/api/v1/autopay/webhooks/"+cfg.String(), body); w.Code != want {
			t.Errorf("webhook error %v -> %d, want %d", err, w.Code, want)
		}
	}
}

func TestWebhookBodyIsCapped(t *testing.T) {
	cfg := uuid.New()
	svc := &stubService{}
	big := bytes.Repeat([]byte("x"), maxWebhookBodyBytes+1)
	req := httptest.NewRequest("POST", "/api/v1/autopay/webhooks/"+cfg.String(), bytes.NewReader(big))
	w := httptest.NewRecorder()
	newRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || svc.gotBody != nil {
		t.Fatalf("an oversized unauthenticated body must be rejected before it reaches the service: code=%d", w.Code)
	}
}
