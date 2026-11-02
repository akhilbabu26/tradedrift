package postgres_test

import (
	"errors"
	"strings"
	"testing"

	"tradedrift/platform/postgres"
)

func TestExtractDSNMetadata(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		wantHost   string
		wantDB     string
		wantErr    bool
		errContain string
	}{
		{
			name:     "valid URL format",
			dsn:      "postgres://user:secret123@aws-db.internal:5432/tradedrift_auth?sslmode=disable",
			wantHost: "aws-db.internal:5432",
			wantDB:   "tradedrift_auth",
			wantErr:  false,
		},
		{
			name:     "valid postgresql scheme",
			dsn:      "postgresql://user:secret123@aws-db.internal:5432/tradedrift_auth",
			wantHost: "aws-db.internal:5432",
			wantDB:   "tradedrift_auth",
			wantErr:  false,
		},
		{
			name:     "valid keyword format",
			dsn:      "host=aws-db.internal port=5432 user=tradedrift dbname=tradedrift_wallet sslmode=require",
			wantHost: "aws-db.internal",
			wantDB:   "tradedrift_wallet",
			wantErr:  false,
		},
		{
			name:     "keyword format with single quotes and spaces in password",
			dsn:      "host='aws-db.internal' port=5432 user='tradedrift' password='my secret password' dbname='tradedrift_wallet'",
			wantHost: "aws-db.internal",
			wantDB:   "tradedrift_wallet",
			wantErr:  false,
		},
		{
			name:     "keyword format with double quotes",
			dsn:      `host="aws-db.internal" port=5432 user="tradedrift" password="my secret password" dbname="tradedrift_wallet"`,
			wantHost: "aws-db.internal",
			wantDB:   "tradedrift_wallet",
			wantErr:  false,
		},
		{
			name:     "URL with reserved characters percent-encoded in password",
			dsn:      "postgres://user:p%40ss%3Aw%2Fo%23r%3Fd%2520%21@aws-db.internal:5432/tradedrift_wallet?sslmode=require",
			wantHost: "aws-db.internal:5432",
			wantDB:   "tradedrift_wallet",
			wantErr:  false,
		},
		{
			name:     "URL with IPv4 without port",
			dsn:      "postgres://user:pass@192.168.1.50/tradedrift_order",
			wantHost: "192.168.1.50",
			wantDB:   "tradedrift_order",
			wantErr:  false,
		},
		{
			name:     "URL with IPv6 with port",
			dsn:      "postgres://user:pass@[2001:db8::1]:5432/tradedrift_settlement",
			wantHost: "[2001:db8::1]:5432",
			wantDB:   "tradedrift_settlement",
			wantErr:  false,
		},
		{
			name:     "URL with IPv6 without port",
			dsn:      "postgres://user:pass@[2001:db8::1]/tradedrift_settlement",
			wantHost: "[2001:db8::1]",
			wantDB:   "tradedrift_settlement",
			wantErr:  false,
		},
		{
			name:     "URL with percent-encoded database name",
			dsn:      "postgres://user:pass@aws-db.internal:5432/tradedrift%5Fwallet",
			wantHost: "aws-db.internal:5432",
			wantDB:   "tradedrift_wallet",
			wantErr:  false,
		},
		{
			name:       "empty DSN",
			dsn:        "",
			wantErr:    true,
			errContain: "empty",
		},
		{
			name:       "whitespace-only DSN",
			dsn:        "    \t  \n  ",
			wantErr:    true,
			errContain: "empty",
		},
		{
			name:       "URL missing database name",
			dsn:        "postgres://user:pass@aws-db.internal:5432",
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "URL with trailing slash but no database name",
			dsn:        "postgres://user:pass@aws-db.internal:5432/",
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "URL missing host",
			dsn:        "postgres:///tradedrift_wallet",
			wantErr:    true,
			errContain: "missing host",
		},
		{
			name:       "URL with multiple path segments",
			dsn:        "postgres://user:pass@aws-db.internal:5432/tradedrift_wallet/extra",
			wantErr:    true,
			errContain: "invalid multiple segments",
		},
		{
			name:       "keyword format missing dbname",
			dsn:        "host=aws-db.internal port=5432 user=tradedrift",
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "keyword format missing host",
			dsn:        "user=tradedrift dbname=tradedrift_wallet port=5432",
			wantErr:    true,
			errContain: "missing host",
		},
		{
			name:       "malformed URL",
			dsn:        "postgres://::invalid-url:not-valid",
			wantErr:    true,
			errContain: "invalid",
		},
		{
			name:       "unsupported random string without keywords",
			dsn:        "just_some_random_text_without_equals",
			wantErr:    true,
			errContain: "malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, db, err := postgres.ExtractDSNMetadata(tt.dsn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractDSNMetadata() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errContain != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errContain)) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContain)
				}
				return
			}
			if h != tt.wantHost {
				t.Errorf("host = %q, want %q", h, tt.wantHost)
			}
			if db != tt.wantDB {
				t.Errorf("db = %q, want %q", db, tt.wantDB)
			}
		})
	}
}

func TestMaskDSN(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		secret     string
		wantMasked string
	}{
		{
			name:       "URL format with standard password",
			dsn:        "postgres://admin_user:superSecretPassword99@rds.ap-south-1.aws.com:5432/tradedrift_auth?sslmode=require",
			secret:     "superSecretPassword99",
			wantMasked: "postgres://admin_user:***@rds.ap-south-1.aws.com:5432/tradedrift_auth?sslmode=require",
		},
		{
			name:       "KV format with simple password",
			dsn:        "host=rds.internal user=admin password=topSecretPass123 dbname=tradedrift_wallet",
			secret:     "topSecretPass123",
			wantMasked: "password=***",
		},
		{
			name:       "KV format with single-quoted password containing spaces",
			dsn:        "host=rds.internal user=admin password='super secret with spaces' dbname=tradedrift_wallet",
			secret:     "super secret with spaces",
			wantMasked: "password=***",
		},
		{
			name:       "KV format with double-quoted password containing spaces",
			dsn:        `host=rds.internal user=admin password="another secret password" dbname=tradedrift_wallet`,
			secret:     "another secret password",
			wantMasked: "password=***",
		},
		{
			name:       "empty DSN returns placeholder",
			dsn:        "",
			wantMasked: "<empty>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masked := postgres.MaskDSN(tt.dsn)
			if tt.secret != "" && strings.Contains(masked, tt.secret) {
				t.Fatalf("MaskDSN leaked secret %q in output: %s", tt.secret, masked)
			}
			if tt.wantMasked != "" && !strings.Contains(masked, tt.wantMasked) {
				t.Errorf("MaskDSN output %q does not contain expected substring %q", masked, tt.wantMasked)
			}
		})
	}
}

func TestValidateDSN(t *testing.T) {
	tests := []struct {
		name       string
		service    string
		expectedDB string
		dsn        string
		isProd     bool
		wantErr    bool
		errContain string
	}{
		{
			name:       "empty DSN",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "",
			isProd:     true,
			wantErr:    true,
			errContain: "empty",
		},
		{
			name:       "placeholder value rejected in prod",
			service:    "wallet",
			expectedDB: "tradedrift_wallet",
			dsn:        "postgres://user:REPLACE_WITH_STRONG_PASSWORD@aws-rds.internal:5432/tradedrift_wallet",
			isProd:     true,
			wantErr:    true,
			errContain: "placeholder",
		},
		{
			name:       "placeholder value rejected in dev",
			service:    "wallet",
			expectedDB: "tradedrift_wallet",
			dsn:        "postgres://user:<CHANGE_ME>@localhost:5432/tradedrift_wallet",
			isProd:     false,
			wantErr:    true,
			errContain: "placeholder",
		},
		{
			name:       "localhost rejected in prod",
			service:    "order",
			expectedDB: "tradedrift_order",
			dsn:        "postgres://user:secret@localhost:5432/tradedrift_order",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "localhost allowed in dev",
			service:    "order",
			expectedDB: "tradedrift_order",
			dsn:        "postgres://user:secret@localhost:5432/tradedrift_order",
			isProd:     false,
			wantErr:    false,
		},
		{
			name:       "127.0.0.1 rejected in prod",
			service:    "trade",
			expectedDB: "tradedrift_trade",
			dsn:        "postgres://user:secret@127.0.0.1:5432/tradedrift_trade",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "127.0.0.2 (loopback subnet) rejected in prod",
			service:    "trade",
			expectedDB: "tradedrift_trade",
			dsn:        "postgres://user:secret@127.0.0.2:5432/tradedrift_trade",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "IPv6 loopback [::1] rejected in prod (URL)",
			service:    "settlement",
			expectedDB: "tradedrift_settlement",
			dsn:        "postgres://user:secret@[::1]:5432/tradedrift_settlement",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "IPv6 loopback ::1 rejected in prod (KV format)",
			service:    "settlement",
			expectedDB: "tradedrift_settlement",
			dsn:        "host=::1 port=5432 user=tradedrift dbname=tradedrift_settlement",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "host.docker.internal rejected in prod",
			service:    "matching-engine",
			expectedDB: "tradedrift_matching",
			dsn:        "postgres://user:secret@host.docker.internal:5432/tradedrift_matching",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "0.0.0.0 unspecified host rejected in prod",
			service:    "order",
			expectedDB: "tradedrift_order",
			dsn:        "postgres://user:secret@0.0.0.0:5432/tradedrift_order",
			isProd:     true,
			wantErr:    true,
			errContain: "loopback",
		},
		{
			name:       "database mismatch rejected",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "postgres://user:secret@aws-rds.internal:5432/tradedrift_wallet",
			isProd:     true,
			wantErr:    true,
			errContain: "mismatch",
		},
		{
			name:       "missing database name in URL rejected",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "postgres://user:secret@aws-rds.internal:5432",
			isProd:     false,
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "missing database name with trailing slash rejected",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "postgres://user:secret@aws-rds.internal:5432/",
			isProd:     false,
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "missing database name in KV format rejected",
			service:    "wallet",
			expectedDB: "tradedrift_wallet",
			dsn:        "host=aws-rds.internal port=5432 user=tradedrift",
			isProd:     false,
			wantErr:    true,
			errContain: "missing database name",
		},
		{
			name:       "case-insensitive database match accepted",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "postgres://user:secret@aws-rds.internal:5432/TRADEDRIFT_AUTH",
			isProd:     true,
			wantErr:    false,
		},
		{
			name:       "percent-encoded path matching expected database accepted",
			service:    "wallet",
			expectedDB: "tradedrift_wallet",
			dsn:        "postgres://user:secret@aws-rds.internal:5432/tradedrift%5Fwallet",
			isProd:     true,
			wantErr:    false,
		},
		{
			name:       "valid production URL DSN accepted",
			service:    "auth",
			expectedDB: "tradedrift_auth",
			dsn:        "postgres://admin:securepass123@tradedrift-db.cluster-c1.ap-south-1.rds.amazonaws.com:5432/tradedrift_auth?sslmode=require",
			isProd:     true,
			wantErr:    false,
		},
		{
			name:       "valid production KV DSN accepted",
			service:    "wallet",
			expectedDB: "tradedrift_wallet",
			dsn:        "host=tradedrift-db.cluster-c1.ap-south-1.rds.amazonaws.com port=5432 user=admin password=securepass123 dbname=tradedrift_wallet sslmode=require",
			isProd:     true,
			wantErr:    false,
		},
		{
			name:       "non-loopback hostname containing localhost substring accepted",
			service:    "portfolio",
			expectedDB: "tradedrift_portfolio",
			dsn:        "postgres://admin:securepass123@notlocalhost.example.internal:5432/tradedrift_portfolio",
			isProd:     true,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := postgres.ValidateDSN(tt.service, tt.expectedDB, tt.dsn, tt.isProd)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateDSN() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContain != "" {
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errContain)) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContain)
				}
			}
		})
	}
}

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name       string
		rawErr     error
		secret     string
		wantMasked string
	}{
		{
			name:       "URL error with password",
			rawErr:     errors.New("failed to connect to postgres://app_user:SuperSecretPassword123@aws-db.com:5432/tradedrift: connection refused"),
			secret:     "SuperSecretPassword123",
			wantMasked: "postgres://app_user:***@",
		},
		{
			name:       "KV error with quoted password containing spaces",
			rawErr:     errors.New("connection failed with password='Secret Password With Spaces' host=aws-db.internal: timeout"),
			secret:     "Secret Password With Spaces",
			wantMasked: "password=***",
		},
		{
			name:       "KV error with unquoted password",
			rawErr:     errors.New("auth failed password=PlainSecretPass dbname=tradedrift_wallet"),
			secret:     "PlainSecretPass",
			wantMasked: "password=***",
		},
		{
			name:       "nil error returns nil",
			rawErr:     nil,
			wantMasked: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := postgres.SanitizeError(tt.rawErr)
			if tt.rawErr == nil {
				if sanitized != nil {
					t.Fatalf("expected nil for nil input, got: %v", sanitized)
				}
				return
			}
			if strings.Contains(sanitized.Error(), tt.secret) {
				t.Fatalf("SanitizeError failed to mask secret %q: %s", tt.secret, sanitized.Error())
			}
			if tt.wantMasked != "" && !strings.Contains(sanitized.Error(), tt.wantMasked) {
				t.Errorf("SanitizeError output %q does not contain %q", sanitized.Error(), tt.wantMasked)
			}
		})
	}
}
