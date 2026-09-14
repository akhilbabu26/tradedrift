package test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	platformerrors "tradedrift/platform/errors"
	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/admin/internal/config"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/handler"
)

func TestPlatformErrorSentinelIdentity(t *testing.T) {
	// 1. Verify errors.Is() matches sentinel
	if !errors.Is(domain.ErrIncidentNotFound, domain.ErrIncidentNotFound) {
		t.Fatal("expected errors.Is to match domain.ErrIncidentNotFound")
	}

	// 2. Verify platform code extraction
	code := platformerrors.GetCode(domain.ErrIncidentNotFound)
	if code != platformerrors.CodeNotFound {
		t.Fatalf("expected CodeNotFound (%s), got: %s", platformerrors.CodeNotFound, code)
	}

	// 3. Verify clean string retention (no prefix pollution)
	expectedMsg := "incident not found"
	if domain.ErrIncidentNotFound.Error() != expectedMsg {
		t.Fatalf("expected error string %q, got: %q", expectedMsg, domain.ErrIncidentNotFound.Error())
	}

	// 4. Verify other sentinels
	if platformerrors.GetCode(domain.ErrOperationInProgress) != platformerrors.CodeFailedPrecondition {
		t.Fatalf("expected FAILED_PRECONDITION for ErrOperationInProgress, got: %s", platformerrors.GetCode(domain.ErrOperationInProgress))
	}
	if platformerrors.GetCode(domain.ErrForbidden) != platformerrors.CodePermissionDenied {
		t.Fatalf("expected PERMISSION_DENIED for ErrForbidden, got: %s", platformerrors.GetCode(domain.ErrForbidden))
	}
	if platformerrors.GetCode(domain.ErrAuthUnavailable) != platformerrors.CodeUnavailable {
		t.Fatalf("expected UNAVAILABLE for ErrAuthUnavailable, got: %s", platformerrors.GetCode(domain.ErrAuthUnavailable))
	}
}

func TestHandleServiceErrorMapping(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
		expectedMsg    string
	}{
		{
			name:           "Not Found",
			err:            domain.ErrIncidentNotFound,
			expectedStatus: http.StatusNotFound,
			expectedCode:   "NOT_FOUND",
			expectedMsg:    "incident not found",
		},
		{
			name:           "Conflict / Failed Precondition",
			err:            domain.ErrOperationInProgress,
			expectedStatus: http.StatusConflict,
			expectedCode:   "FAILED_PRECONDITION",
			expectedMsg:    "operation in progress: same idempotency key is currently being processed",
		},
		{
			name:           "Invalid Argument",
			err:            domain.ErrInvalidReason,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "INVALID_ARGUMENT",
			expectedMsg:    "reason is required and must be 5-500 characters",
		},
		{
			name:           "Forbidden",
			err:            domain.ErrForbidden,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "PERMISSION_DENIED",
			expectedMsg:    "forbidden: admin role required",
		},
		{
			name:           "Unavailable",
			err:            domain.ErrAuthUnavailable,
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "UNAVAILABLE",
			expectedMsg:    "auth service temporarily unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.HandleServiceError(rr, tc.err, nil)

			if rr.Code != tc.expectedStatus {
				t.Errorf("status mismatch: expected %d, got %d", tc.expectedStatus, rr.Code)
			}

			var resp handler.ErrorResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode JSON response: %v", err)
			}

			if resp.Error != tc.expectedMsg {
				t.Errorf("error msg mismatch: expected %q, got %q", tc.expectedMsg, resp.Error)
			}
			if resp.Code != tc.expectedCode {
				t.Errorf("code mismatch: expected %q, got %q", tc.expectedCode, resp.Code)
			}
		})
	}
}

func TestConfigValidation_FailFast(t *testing.T) {
	// Clean env for deterministic testing
	origJWT := os.Getenv("JWT_SECRET")
	origDSN := os.Getenv("POSTGRES_DSN")
	origKafka := os.Getenv("KAFKA_BROKERS")
	origOutbox := os.Getenv("ADMIN_OUTBOX_INTERVAL")
	defer func() {
		_ = os.Setenv("JWT_SECRET", origJWT)
		_ = os.Setenv("POSTGRES_DSN", origDSN)
		_ = os.Setenv("KAFKA_BROKERS", origKafka)
		_ = os.Setenv("ADMIN_OUTBOX_INTERVAL", origOutbox)
	}()

	// 1. Missing JWT_SECRET should fail fast
	_ = os.Unsetenv("JWT_SECRET")
	_ = os.Setenv("POSTGRES_DSN", "postgres://localhost:5432/test")
	_ = os.Setenv("KAFKA_BROKERS", "localhost:9092")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when JWT_SECRET is missing, got nil")
	}

	// 2. Out-of-bounds duration should fail fast
	_ = os.Setenv("JWT_SECRET", "test-secret")
	_ = os.Setenv("ADMIN_OUTBOX_INTERVAL", "1ms") // below 100ms min
	_, err = config.Load()
	if err == nil {
		t.Fatal("expected error for interval below minimum, got nil")
	}

	// 3. Valid configuration loads cleanly
	_ = os.Setenv("ADMIN_OUTBOX_INTERVAL", "2s")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected valid config to load, got: %v", err)
	}
	if cfg.OutboxInterval != 2*time.Second {
		t.Fatalf("expected 2s OutboxInterval, got: %v", cfg.OutboxInterval)
	}
}

func TestUUIDPlatformDelegation(t *testing.T) {
	id := domain.MustNewV7()
	if len(id) != 36 {
		t.Fatalf("expected standard UUID length 36, got: %d (%s)", len(id), id)
	}

	// Verify it can also be generated directly from platformuuid
	platformID, err := platformuuid.New()
	if err != nil {
		t.Fatalf("platformuuid.New failed: %v", err)
	}
	if len(platformID) != 36 {
		t.Fatalf("expected platform UUID length 36, got: %d", len(platformID))
	}
}
