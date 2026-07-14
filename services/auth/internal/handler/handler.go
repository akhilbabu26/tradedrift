package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	platformerrors "tradedrift/platform/errors"
	platformjwt "tradedrift/platform/jwt"
	authv1 "tradedrift/platform/api/gen/auth/v1"
	"tradedrift/services/auth/internal/service"

	"go.uber.org/zap"
)

type GRPCHandler struct {
	authv1.UnimplementedAuthServiceServer
	svc *service.Service
	log *zap.Logger
}

// NewGRPCHandler creates a new gRPC handler instance.
func NewGRPCHandler(svc *service.Service, log *zap.Logger) *GRPCHandler {
	return &GRPCHandler{svc: svc, log: log}
}

func (h *GRPCHandler) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	userID, err := h.svc.Register(ctx, req.Email, req.Username, req.Password)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.RegisterResponse{
		UserId:               userID,
		VerificationRequired: true,
	}, nil
}

func (h *GRPCHandler) VerifyEmail(ctx context.Context, req *authv1.VerifyEmailRequest) (*authv1.VerifyEmailResponse, error) {
	u, tp, err := h.svc.VerifyEmail(ctx, req.Email, req.Code)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.VerifyEmailResponse{
		User: &authv1.User{
			UserId:   u.ID,
			Email:    u.Email,
			Username: u.Username,
		},
		AccessToken:           tp.AccessToken,
		RefreshToken:          tp.RefreshToken,
		AccessTokenExpiresAt:  timestamppb.New(tp.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: timestamppb.New(tp.RefreshTokenExpiresAt),
	}, nil
}

func (h *GRPCHandler) ResendVerificationCode(ctx context.Context, req *authv1.ResendVerificationCodeRequest) (*authv1.ResendVerificationCodeResponse, error) {
	err := h.svc.ResendVerificationCode(ctx, req.Email)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.ResendVerificationCodeResponse{
		Success: true,
	}, nil
}

func (h *GRPCHandler) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	u, tp, err := h.svc.Login(ctx, req.Identifier, req.Password)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.LoginResponse{
		User: &authv1.User{
			UserId:   u.ID,
			Email:    u.Email,
			Username: u.Username,
		},
		AccessToken:           tp.AccessToken,
		RefreshToken:          tp.RefreshToken,
		AccessTokenExpiresAt:  timestamppb.New(tp.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: timestamppb.New(tp.RefreshTokenExpiresAt),
	}, nil
}

func (h *GRPCHandler) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	tp, err := h.svc.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.RefreshTokenResponse{
		AccessToken:           tp.AccessToken,
		RefreshToken:          tp.RefreshToken,
		AccessTokenExpiresAt:  timestamppb.New(tp.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: timestamppb.New(tp.RefreshTokenExpiresAt),
	}, nil
}

func (h *GRPCHandler) ForgotPassword(ctx context.Context, req *authv1.ForgotPasswordRequest) (*authv1.ForgotPasswordResponse, error) {
	err := h.svc.ForgotPassword(ctx, req.Email)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.ForgotPasswordResponse{
		Success: true,
	}, nil
}

func (h *GRPCHandler) ResetPassword(ctx context.Context, req *authv1.ResetPasswordRequest) (*authv1.ResetPasswordResponse, error) {
	err := h.svc.ResetPassword(ctx, req.Email, req.Code, req.NewPassword)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.ResetPasswordResponse{
		Success: true,
	}, nil
}

func (h *GRPCHandler) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	// Extract the authenticated user details and token JTI from the context.
	// In downstream microservices, the platform gRPC Auth interceptor injects these claims.
	claims, ok := platformjwt.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated request context")
	}

	err := h.svc.Logout(ctx, req.RefreshToken, claims.JTI, claims.UserID, claims.ExpiresAt.Time)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.LogoutResponse{
		Success: true,
	}, nil
}

func (h *GRPCHandler) LogoutAll(ctx context.Context, req *authv1.LogoutAllRequest) (*authv1.LogoutAllResponse, error) {
	claims, ok := platformjwt.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated request context")
	}

	err := h.svc.LogoutAll(ctx, claims.UserID)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.LogoutAllResponse{
		Success: true,
	}, nil
}

func (h *GRPCHandler) ChangePassword(ctx context.Context, req *authv1.ChangePasswordRequest) (*authv1.ChangePasswordResponse, error) {
	claims, ok := platformjwt.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthenticated request context")
	}

	err := h.svc.ChangePassword(ctx, claims.UserID, req.OldPassword, req.NewPassword)
	if err != nil {
		return nil, mapToGRPCError(err)
	}

	return &authv1.ChangePasswordResponse{
		Success: true,
	}, nil
}

// mapToGRPCError converts a structured platform error to standard gRPC statuses.
func mapToGRPCError(err error) error {
	if err == nil {
		return nil
	}

	var pErr *platformerrors.PlatformError
	if errors.As(err, &pErr) {
		switch pErr.Code {
		case platformerrors.CodeInvalidArgument:
			return status.Error(codes.InvalidArgument, pErr.Message)
		case platformerrors.CodeInvalidCredentials:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeTokenExpired:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeTokenRevoked:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeAccountNotActive:
			return status.Error(codes.FailedPrecondition, pErr.Message)
		case platformerrors.CodeAlreadyExists:
			return status.Error(codes.AlreadyExists, pErr.Message)
		case platformerrors.CodeNotFound:
			return status.Error(codes.NotFound, pErr.Message)
		case platformerrors.CodePermissionDenied:
			return status.Error(codes.PermissionDenied, pErr.Message)
		case platformerrors.CodeFailedPrecondition:
			return status.Error(codes.FailedPrecondition, pErr.Message)
		default:
			return status.Error(codes.Internal, pErr.Message)
		}
	}

	// Fallback for default errors
	return status.Errorf(codes.Internal, "internal server error: %v", err)
}
