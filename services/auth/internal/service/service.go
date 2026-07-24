package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"golang.org/x/crypto/bcrypt"

	"tradedrift/services/auth/internal/mail"
	"tradedrift/services/auth/internal/otp"
	"tradedrift/services/auth/internal/repository"
)

var (
	emailRegex = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)
	bcryptSem  = make(chan struct{}, runtime.NumCPU())
)

// WalletClient defines the interface for communicating with the Wallet Service.
type WalletClient interface {
	InitializeWallet(ctx context.Context, userID string) error
}

// Service contains the dependencies and handlers for core auth logic.
type Service struct {
	userRepo   repository.UserRepository
	tokenRepo  repository.RefreshTokenRepository
	otpMgr     *otp.Manager
	mailer     mail.Mailer
	walletCl   WalletClient
	rdb        *redis.Client
	jwtSecret  []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	log        *zap.Logger
}

// NewService creates a new Service instance.
func NewService(
	userRepo repository.UserRepository,
	tokenRepo repository.RefreshTokenRepository,
	otpMgr *otp.Manager,
	mailer mail.Mailer,
	walletCl WalletClient,
	rdb *redis.Client,
	jwtSecret []byte,
	accessTTL time.Duration,
	refreshTTL time.Duration,
	log *zap.Logger,
) *Service {
	return &Service{
		userRepo:   userRepo,
		tokenRepo:  tokenRepo,
		otpMgr:     otpMgr,
		mailer:     mailer,
		walletCl:   walletCl,
		rdb:        rdb,
		jwtSecret:  jwtSecret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		log:        log,
	}
}

// UserDTO is the user payload returned on login/verification.
type UserDTO struct {
	ID       string
	Email    string
	Username string
}

// TokenPairDTO holds the generated tokens and expirations.
type TokenPairDTO struct {
	AccessToken           string
	RefreshToken          string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

// ==========================================
// Internal Helpers
// ==========================================

func (s *Service) issueTokenPair(ctx context.Context, userID, email string, tokenVersion int) (*TokenPairDTO, error) {
	now := time.Now().UTC()

	// Issue Access Token
	accessToken, _, err := platformjwt.IssueAccessToken(userID, email, tokenVersion, s.jwtSecret, s.accessTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to issue access token: %w", err)
	}

	// Issue Refresh Token
	rawRefresh, hashRefresh, err := platformjwt.IssueRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to issue refresh token: %w", err)
	}

	// Save Refresh Token to Postgres
	tokenRecord := &repository.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		TokenHash: hashRefresh,
		Status:    "ACTIVE",
		ExpiresAt: now.Add(s.refreshTTL),
		CreatedAt: now,
	}

	err = s.tokenRepo.Create(ctx, tokenRecord)
	if err != nil {
		return nil, fmt.Errorf("failed to save refresh token in db: %w", err)
	}

	return &TokenPairDTO{
		AccessToken:           accessToken,
		RefreshToken:          rawRefresh,
		AccessTokenExpiresAt:  now.Add(s.accessTTL),
		RefreshTokenExpiresAt: now.Add(s.refreshTTL),
	}, nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// GetTokenVersion implements platformjwt.AuthProvider interface for local JWT validation.
func (s *Service) GetTokenVersion(ctx context.Context, userID string) (int, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	if u == nil {
		return 0, fmt.Errorf("user not found")
	}
	return u.TokenVersion, nil
}

// IsTokenBlacklisted implements platformjwt.AuthProvider interface for local JWT validation.
func (s *Service) IsTokenBlacklisted(ctx context.Context, jti string) (bool, error) {
	return s.tokenRepo.IsTokenBlacklisted(ctx, jti)
}

// compareBcrypt runs bcrypt comparison with CPU-bounded concurrency.
func (s *Service) compareBcrypt(hash, password string) error {
	bcryptSem <- struct{}{}
	defer func() { <-bcryptSem }()
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// hashBcrypt runs bcrypt hashing with CPU-bounded concurrency.
func (s *Service) hashBcrypt(password string) ([]byte, error) {
	bcryptSem <- struct{}{}
	defer func() { <-bcryptSem }()
	return bcrypt.GenerateFromPassword([]byte(password), 10)
}

