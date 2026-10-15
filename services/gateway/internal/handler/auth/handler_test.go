package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "tradedrift/platform/api/gen/auth/v1"
	authhandler "tradedrift/services/gateway/internal/handler/auth"
)

type mockAuthClient struct {
	authv1.AuthServiceClient
	googleLoginFunc          func(ctx context.Context, in *authv1.GoogleLoginRequest, opts ...grpc.CallOption) (*authv1.GoogleLoginResponse, error)
	linkGoogleAccountFunc    func(ctx context.Context, in *authv1.LinkGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.LinkGoogleAccountResponse, error)
	connectGoogleAccountFunc func(ctx context.Context, in *authv1.ConnectGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.ConnectGoogleAccountResponse, error)
}

func (m *mockAuthClient) GoogleLogin(ctx context.Context, in *authv1.GoogleLoginRequest, opts ...grpc.CallOption) (*authv1.GoogleLoginResponse, error) {
	if m.googleLoginFunc != nil {
		return m.googleLoginFunc(ctx, in, opts...)
	}
	return nil, nil
}

func (m *mockAuthClient) LinkGoogleAccount(ctx context.Context, in *authv1.LinkGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.LinkGoogleAccountResponse, error) {
	if m.linkGoogleAccountFunc != nil {
		return m.linkGoogleAccountFunc(ctx, in, opts...)
	}
	return nil, nil
}

func (m *mockAuthClient) ConnectGoogleAccount(ctx context.Context, in *authv1.ConnectGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.ConnectGoogleAccountResponse, error) {
	if m.connectGoogleAccountFunc != nil {
		return m.connectGoogleAccountFunc(ctx, in, opts...)
	}
	return nil, nil
}

func TestHandler_GoogleLogin_Success(t *testing.T) {
	now := time.Now().UTC()
	client := &mockAuthClient{
		googleLoginFunc: func(ctx context.Context, in *authv1.GoogleLoginRequest, opts ...grpc.CallOption) (*authv1.GoogleLoginResponse, error) {
			if in.IdToken != "valid_id_token" {
				t.Errorf("expected id_token 'valid_id_token', got %s", in.IdToken)
			}
			return &authv1.GoogleLoginResponse{
				Status: authv1.GoogleLoginStatus_GOOGLE_LOGIN_STATUS_SUCCESS,
				User: &authv1.User{
					UserId:   "user-123",
					Email:    "trader@test.com",
					Username: "trader_123",
				},
				AccessToken:           "access-token-xyz",
				RefreshToken:          "refresh-token-xyz",
				AccessTokenExpiresAt:  timestamppb.New(now.Add(15 * time.Minute)),
				RefreshTokenExpiresAt: timestamppb.New(now.Add(7 * 24 * time.Hour)),
			}, nil
		},
	}

	h := authhandler.NewHandler(client)

	reqBody, _ := json.Marshal(authhandler.GoogleLoginRequestDTO{
		IDToken: "valid_id_token",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.GoogleLogin(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify session cookies were set
	cookies := resp.Cookies()
	var accessCookie, refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "access_token" {
			accessCookie = c
		}
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}

	if accessCookie == nil || accessCookie.Value != "access-token-xyz" || !accessCookie.HttpOnly {
		t.Errorf("expected valid HttpOnly access_token cookie, got: %v", accessCookie)
	}
	if refreshCookie == nil || refreshCookie.Value != "refresh-token-xyz" || !refreshCookie.HttpOnly {
		t.Errorf("expected valid HttpOnly refresh_token cookie, got: %v", refreshCookie)
	}

	var resDTO authhandler.GoogleLoginResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&resDTO); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resDTO.Status != "SUCCESS" || resDTO.User.Email != "trader@test.com" {
		t.Errorf("unexpected response body: %+v", resDTO)
	}
}

func TestHandler_GoogleLogin_LinkRequired(t *testing.T) {
	client := &mockAuthClient{
		googleLoginFunc: func(ctx context.Context, in *authv1.GoogleLoginRequest, opts ...grpc.CallOption) (*authv1.GoogleLoginResponse, error) {
			return &authv1.GoogleLoginResponse{
				Status:    authv1.GoogleLoginStatus_GOOGLE_LOGIN_STATUS_LINK_REQUIRED,
				LinkToken: "link-token-uuid",
				Email:     "existing@test.com",
				Message:   "Please enter password to link account",
			}, nil
		},
	}

	h := authhandler.NewHandler(client)

	reqBody, _ := json.Marshal(authhandler.GoogleLoginRequestDTO{
		IDToken: "valid_id_token",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.GoogleLogin(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Cookies should NOT be set when linking is required
	if len(resp.Cookies()) > 0 {
		t.Errorf("expected no cookies on LINK_REQUIRED, got %v", resp.Cookies())
	}

	var resDTO authhandler.GoogleLoginResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&resDTO); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resDTO.Status != "LINK_REQUIRED" || resDTO.LinkToken != "link-token-uuid" || resDTO.Email != "existing@test.com" {
		t.Errorf("unexpected link required body: %+v", resDTO)
	}
}

func TestHandler_LinkGoogleAccount(t *testing.T) {
	now := time.Now().UTC()
	client := &mockAuthClient{
		linkGoogleAccountFunc: func(ctx context.Context, in *authv1.LinkGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.LinkGoogleAccountResponse, error) {
			if in.Email != "existing@test.com" || in.Password != "pass123" || in.LinkToken != "link-123" {
				t.Errorf("unexpected link request: %+v", in)
			}
			return &authv1.LinkGoogleAccountResponse{
				User: &authv1.User{
					UserId:   "user-linked-456",
					Email:    "existing@test.com",
					Username: "existing_user",
				},
				AccessToken:           "new-access-token",
				RefreshToken:          "new-refresh-token",
				AccessTokenExpiresAt:  timestamppb.New(now.Add(15 * time.Minute)),
				RefreshTokenExpiresAt: timestamppb.New(now.Add(7 * 24 * time.Hour)),
			}, nil
		},
	}

	h := authhandler.NewHandler(client)

	reqBody, _ := json.Marshal(authhandler.LinkGoogleRequestDTO{
		Email:     "existing@test.com",
		Password:  "pass123",
		LinkToken: "link-123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google/link", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.LinkGoogleAccount(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var resDTO authhandler.AuthResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&resDTO); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resDTO.User.UserID != "user-linked-456" {
		t.Errorf("unexpected user ID in link response: %s", resDTO.User.UserID)
	}
}

func TestHandler_ConnectGoogleAccount(t *testing.T) {
	client := &mockAuthClient{
		connectGoogleAccountFunc: func(ctx context.Context, in *authv1.ConnectGoogleAccountRequest, opts ...grpc.CallOption) (*authv1.ConnectGoogleAccountResponse, error) {
			return &authv1.ConnectGoogleAccountResponse{
				Success:  true,
				Provider: "google",
				Email:    "connected@google.com",
			}, nil
		},
	}

	h := authhandler.NewHandler(client)

	reqBody, _ := json.Marshal(authhandler.ConnectGoogleRequestDTO{
		IDToken: "valid_id_token",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google/connect", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.ConnectGoogleAccount(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var resDTO authhandler.ConnectGoogleResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&resDTO); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resDTO.Success || resDTO.Email != "connected@google.com" {
		t.Errorf("unexpected connect response: %+v", resDTO)
	}
}
