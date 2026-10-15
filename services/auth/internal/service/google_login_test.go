package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"tradedrift/services/auth/internal/google"
	"tradedrift/services/auth/internal/repository"
)

// MockTokenValidator implements google.TokenValidator for unit testing.
type mockTokenValidator struct {
	payload *google.TokenPayload
	err     error
}

func (m *mockTokenValidator) Validate(ctx context.Context, rawIDToken string) (*google.TokenPayload, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.payload, nil
}

// MockIdentityRepo implements repository.IdentityRepository in memory.
type mockIdentityRepo struct {
	identities map[string]*repository.UserAuthIdentity // key: provider:provider_user_id
	byUser     map[string]*repository.UserAuthIdentity // key: user_id:provider
	userRepo   *mockUserRepo
	createErr  error
}

func newMockIdentityRepo(userRepo *mockUserRepo) *mockIdentityRepo {
	return &mockIdentityRepo{
		identities: make(map[string]*repository.UserAuthIdentity),
		byUser:     make(map[string]*repository.UserAuthIdentity),
		userRepo:   userRepo,
	}
}

func (m *mockIdentityRepo) Create(ctx context.Context, id *repository.UserAuthIdentity) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.identities[id.Provider+":"+id.ProviderUserID] = id
	m.byUser[id.UserID+":"+id.Provider] = id
	return nil
}

func (m *mockIdentityRepo) CreateUserWithIdentity(ctx context.Context, u *repository.User, id *repository.UserAuthIdentity, outboxEventID string, outboxPayload []byte) error {
	if m.createErr != nil {
		return m.createErr
	}
	if m.userRepo != nil {
		m.userRepo.users[u.ID] = u
		m.userRepo.users[u.Email] = u
	}
	m.identities[id.Provider+":"+id.ProviderUserID] = id
	m.byUser[id.UserID+":"+id.Provider] = id
	return nil
}

func (m *mockIdentityRepo) GetByProviderAndUID(ctx context.Context, provider, providerUserID string) (*repository.UserAuthIdentity, error) {
	return m.identities[provider+":"+providerUserID], nil
}

func (m *mockIdentityRepo) GetByUserAndProvider(ctx context.Context, userID, provider string) (*repository.UserAuthIdentity, error) {
	return m.byUser[userID+":"+provider], nil
}

// MockUserRepo implements repository.UserRepository in memory.
type mockUserRepo struct {
	users map[string]*repository.User // key: id or email
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[string]*repository.User)}
}

func (m *mockUserRepo) Create(ctx context.Context, u *repository.User) error {
	m.users[u.ID] = u
	m.users[u.Email] = u
	return nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*repository.User, error) {
	return m.users[id], nil
}

func (m *mockUserRepo) GetByIdentifier(ctx context.Context, identifier string) (*repository.User, error) {
	identLower := strings.ToLower(identifier)
	for _, u := range m.users {
		if strings.ToLower(u.Email) == identLower || u.Username == identifier {
			return u, nil
		}
	}
	return nil, nil
}

func (m *mockUserRepo) VerifyEmail(ctx context.Context, email string, verifiedAt time.Time, outboxEventID string, outboxPayload []byte) error {
	return nil
}

func (m *mockUserRepo) IncrementTokenVersion(ctx context.Context, id string) error {
	if u, ok := m.users[id]; ok {
		u.TokenVersion++
	}
	return nil
}

func (m *mockUserRepo) TrackFailedLogin(ctx context.Context, id string, attempts int, lockUntil *time.Time) error {
	if u, ok := m.users[id]; ok {
		u.FailedLoginAttempts = attempts
		u.LockedUntil = lockUntil
	}
	return nil
}

func (m *mockUserRepo) ResetFailedLogin(ctx context.Context, id string, loginTime time.Time) error {
	if u, ok := m.users[id]; ok {
		u.FailedLoginAttempts = 0
		u.LockedUntil = nil
		u.LastLoginAt = &loginTime
	}
	return nil
}

func (m *mockUserRepo) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	if u, ok := m.users[id]; ok {
		u.PasswordHash = passwordHash
	}
	return nil
}

func (m *mockUserRepo) UpdateStatus(ctx context.Context, id string, status string) error {
	if u, ok := m.users[id]; ok {
		u.Status = status
	}
	return nil
}

func (m *mockUserRepo) ListSuspendedUserIDs(ctx context.Context, limit, offset int) ([]string, int, error) {
	return nil, 0, nil
}

// MockWalletClient implements WalletClient.
type mockWalletClient struct {
	initializedUsers []string
	failErr          error
}

func (m *mockWalletClient) InitializeWallet(ctx context.Context, userID string) error {
	if m.failErr != nil {
		return m.failErr
	}
	m.initializedUsers = append(m.initializedUsers, userID)
	return nil
}

// MockTokenRepo implements repository.RefreshTokenRepository.
type mockTokenRepo struct {
	tokens map[string]*repository.RefreshToken
}

func newMockTokenRepo() *mockTokenRepo {
	return &mockTokenRepo{tokens: make(map[string]*repository.RefreshToken)}
}

func (m *mockTokenRepo) Create(ctx context.Context, t *repository.RefreshToken) error {
	m.tokens[t.TokenHash] = t
	return nil
}

func (m *mockTokenRepo) GetByHash(ctx context.Context, hash string) (*repository.RefreshToken, error) {
	return m.tokens[hash], nil
}

func (m *mockTokenRepo) Rotate(ctx context.Context, oldID string, newToken *repository.RefreshToken) error {
	m.tokens[newToken.TokenHash] = newToken
	return nil
}

func (m *mockTokenRepo) Revoke(ctx context.Context, id string) error {
	return nil
}

func (m *mockTokenRepo) RevokeAll(ctx context.Context, userID string) error {
	return nil
}

func (m *mockTokenRepo) BlacklistToken(ctx context.Context, jti string, userID string, expiresAt time.Time) error {
	return nil
}

func (m *mockTokenRepo) IsTokenBlacklisted(ctx context.Context, jti string) (bool, error) {
	return false, nil
}

func setupTestService(t *testing.T, validator google.TokenValidator) (*Service, *mockUserRepo, *mockIdentityRepo, *mockWalletClient, *redis.Client) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	userRepo := newMockUserRepo()
	identRepo := newMockIdentityRepo(userRepo)
	tokenRepo := newMockTokenRepo()
	walletCl := &mockWalletClient{}

	svc := NewService(
		userRepo,
		identRepo,
		tokenRepo,
		nil,
		nil,
		walletCl,
		validator,
		rdb,
		[]byte("super-secret-jwt-key-for-test-32b!"),
		15*time.Minute,
		7*24*time.Hour,
		zap.NewNop(),
	)

	return svc, userRepo, identRepo, walletCl, rdb
}

func TestGoogleLogin_SubFirst_ExistingLinkedUser(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_123",
			Email:         "trader@test.com",
			EmailVerified: true,
			Name:          "Test Trader",
		},
	}

	svc, userRepo, identRepo, _, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	// Setup existing user
	userID := uuid.NewString()
	userRepo.Create(ctx, &repository.User{
		ID:           userID,
		Email:        "trader@test.com",
		Username:     "trader",
		PasswordHash: "somehash",
		TokenVersion: 1,
		Role:         "user",
		Status:       "VERIFIED",
	})

	identRepo.Create(ctx, &repository.UserAuthIdentity{
		ID:             uuid.NewString(),
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_sub_123",
		Email:          "trader@test.com",
	})

	user, tokens, linkReq, err := svc.GoogleLogin(ctx, "valid_token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if linkReq != nil {
		t.Fatalf("expected direct login, got link_required: %+v", linkReq)
	}
	if user.ID != userID {
		t.Errorf("expected user ID %s, got %s", userID, user.ID)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Errorf("expected session tokens to be populated")
	}
}

func TestGoogleLogin_ReturningUser_WalletFailure_FailsClosed(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_wallet_check",
			Email:         "walletcheck@test.com",
			EmailVerified: true,
			Name:          "Wallet Check",
		},
	}

	svc, userRepo, identRepo, walletCl, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	userID := uuid.NewString()
	userRepo.Create(ctx, &repository.User{
		ID:       userID,
		Email:    "walletcheck@test.com",
		Username: "wallet_check_user",
		Status:   "VERIFIED",
	})
	identRepo.Create(ctx, &repository.UserAuthIdentity{
		ID:             uuid.NewString(),
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_sub_wallet_check",
		Email:          "walletcheck@test.com",
	})

	walletCl.failErr = errors.New("wallet service unavailable")

	_, _, _, err := svc.GoogleLogin(ctx, "valid_token")
	if err == nil {
		t.Fatalf("expected error when returning user wallet verification fails, got nil")
	}
	if !strings.Contains(err.Error(), "wallet verification failed") {
		t.Errorf("expected wallet failure error message, got: %v", err)
	}
}

func TestGoogleLogin_PendingAccount_Rejected(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_pending_111",
			Email:         "pending@test.com",
			EmailVerified: true,
			Name:          "Pending Trader",
		},
	}

	svc, userRepo, identRepo, _, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	userID := uuid.NewString()
	userRepo.Create(ctx, &repository.User{
		ID:       userID,
		Email:    "pending@test.com",
		Username: "pending_trader",
		Status:   "PENDING_VERIFICATION",
	})
	identRepo.Create(ctx, &repository.UserAuthIdentity{
		ID:             uuid.NewString(),
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_sub_pending_111",
		Email:          "pending@test.com",
	})

	_, _, _, err := svc.GoogleLogin(ctx, "valid_token")
	if err == nil {
		t.Fatalf("expected error for PENDING_VERIFICATION account, got nil")
	}
	if !strings.Contains(err.Error(), "account is not verified") {
		t.Errorf("expected verification error, got: %v", err)
	}
}

func TestGoogleLogin_EmailExists_RequiresLink(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_new_456",
			Email:         "Existing@Test.com", // Test case normalization
			EmailVerified: true,
			Name:          "Existing User",
		},
	}

	svc, userRepo, _, _, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	// Existing password user (lowercase stored)
	userID := uuid.NewString()
	hash, _ := bcrypt.GenerateFromPassword([]byte("StrongPass123!"), bcrypt.DefaultCost)
	userRepo.Create(ctx, &repository.User{
		ID:           userID,
		Email:        "existing@test.com",
		Username:     "existing_trader",
		PasswordHash: string(hash),
		TokenVersion: 1,
		Role:         "user",
		Status:       "VERIFIED",
	})

	user, tokens, linkReq, err := svc.GoogleLogin(ctx, "valid_token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != nil || tokens != nil {
		t.Errorf("expected no immediate login session, user=%v, tokens=%v", user, tokens)
	}
	if linkReq == nil {
		t.Fatalf("expected LinkRequiredDTO to be returned")
	}
	if linkReq.Email != "existing@test.com" || linkReq.LinkToken == "" {
		t.Errorf("invalid linkReq payload: %+v", linkReq)
	}

	// Test 1: Wrong password should NOT delete the token
	_, _, badPassErr := svc.LinkGoogleAccount(ctx, "existing@test.com", "WrongPassword!", linkReq.LinkToken)
	if badPassErr == nil {
		t.Fatalf("expected error on wrong password")
	}

	// Test 2: Correct password succeeds with the same token!
	linkedUser, linkedTokens, linkErr := svc.LinkGoogleAccount(ctx, "existing@test.com", "StrongPass123!", linkReq.LinkToken)
	if linkErr != nil {
		t.Fatalf("LinkGoogleAccount failed on correct retry: %v", linkErr)
	}
	if linkedUser.ID != userID {
		t.Errorf("expected linked user ID %s, got %s", userID, linkedUser.ID)
	}
	if linkedTokens.AccessToken == "" {
		t.Errorf("expected access token after linking")
	}

	// Test 3: Token is now consumed; reuse must fail
	_, _, reuseErr := svc.LinkGoogleAccount(ctx, "existing@test.com", "StrongPass123!", linkReq.LinkToken)
	if reuseErr == nil {
		t.Fatalf("expected token reuse to fail")
	}
}

func TestLinkGoogleAccount_DBFailure_KeepsTokenForRetry(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_db_fail",
			Email:         "dbfail@test.com",
			EmailVerified: true,
			Name:          "DB Fail",
		},
	}

	svc, userRepo, identRepo, _, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	userID := uuid.NewString()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Pass12345!"), bcrypt.DefaultCost)
	userRepo.Create(ctx, &repository.User{
		ID:           userID,
		Email:        "dbfail@test.com",
		Username:     "dbfail_trader",
		PasswordHash: string(hash),
		TokenVersion: 1,
		Role:         "user",
		Status:       "VERIFIED",
	})

	_, _, linkReq, _ := svc.GoogleLogin(ctx, "valid_token")
	if linkReq == nil {
		t.Fatalf("expected linkReq")
	}

	// Simulate transient DB failure during identity insertion
	identRepo.createErr = errors.New("simulated transient database outage")

	_, _, failErr := svc.LinkGoogleAccount(ctx, "dbfail@test.com", "Pass12345!", linkReq.LinkToken)
	if failErr == nil {
		t.Fatalf("expected error when DB fails")
	}

	// Now recover DB
	identRepo.createErr = nil

	// The token must STILL be valid in Redis because the protocol is recoverable!
	linkedUser, linkedTokens, retryErr := svc.LinkGoogleAccount(ctx, "dbfail@test.com", "Pass12345!", linkReq.LinkToken)
	if retryErr != nil {
		t.Fatalf("expected retry to succeed after DB recovered, got: %v", retryErr)
	}
	if linkedUser.ID != userID || linkedTokens.AccessToken == "" {
		t.Errorf("unexpected linked user or tokens")
	}
}

func TestGoogleLogin_BrandNewUser_InitializesWallet(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_brand_new_789",
			Email:         "newtrader@test.com",
			EmailVerified: true,
			Name:          "New Trader",
		},
	}

	svc, userRepo, identRepo, walletCl, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	user, tokens, linkReq, err := svc.GoogleLogin(ctx, "valid_token")
	if err != nil {
		t.Fatalf("GoogleLogin failed: %v", err)
	}
	if linkReq != nil {
		t.Fatalf("expected direct signup/login, got link_required: %+v", linkReq)
	}
	if user.Email != "newtrader@test.com" {
		t.Errorf("expected user email newtrader@test.com, got %s", user.Email)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Errorf("expected session tokens to be populated")
	}

	// Verify user was stored with VERIFIED status
	storedUser, _ := userRepo.GetByID(ctx, user.ID)
	if storedUser == nil || storedUser.Status != "VERIFIED" {
		t.Errorf("expected user in repo with VERIFIED status, got %v", storedUser)
	}

	// Verify identity was stored
	ident, _ := identRepo.GetByProviderAndUID(ctx, "google", "google_sub_brand_new_789")
	if ident == nil || ident.UserID != user.ID {
		t.Errorf("expected identity mapped to user %s, got %v", user.ID, ident)
	}

	// Verify wallet was initialized
	if len(walletCl.initializedUsers) != 1 || walletCl.initializedUsers[0] != user.ID {
		t.Errorf("expected wallet initialized once for %s, got %v", user.ID, walletCl.initializedUsers)
	}
}

func TestGoogleLogin_WalletFailure_FailsClosed(t *testing.T) {
	ctx := context.Background()
	validator := &mockTokenValidator{
		payload: &google.TokenPayload{
			Subject:       "google_sub_wallet_fail",
			Email:         "walletfail@test.com",
			EmailVerified: true,
			Name:          "Wallet Fail",
		},
	}

	svc, _, _, walletCl, rdb := setupTestService(t, validator)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for integration test, skipping")
	}

	walletCl.failErr = errors.New("wallet service down")

	_, _, _, err := svc.GoogleLogin(ctx, "valid_token")
	if err == nil {
		t.Fatalf("expected error when wallet initialization fails, got nil")
	}
	if !strings.Contains(err.Error(), "starter wallet initialization failed") {
		t.Errorf("expected wallet failure error message, got: %v", err)
	}
}
