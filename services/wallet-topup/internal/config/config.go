package config

import (
	"os"
	"strconv"
	"time"

	platformconfig "tradedrift/platform/config"
)

type Config struct {
	Port                  string
	PostgresDSN           string
	WalletGRPCAddr        string
	DailyLimitINR         int64
	WebhookSecret         string
	PaymentProvider       string
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
	ReconcilerInterval    time.Duration
	ExpiryInterval        time.Duration
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	postgresDSN, err := platformconfig.GetDSN("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift_topup?sslmode=disable")
	if err != nil {
		panic(err)
	}

	walletGRPCAddr, err := platformconfig.GetAddress("WALLET_GRPC_ADDR", "localhost:50052")
	if err != nil {
		panic(err)
	}

	dailyLimit := int64(10)
	if dlStr := os.Getenv("DAILY_LIMIT_INR"); dlStr != "" {
		if val, err := strconv.ParseInt(dlStr, 10, 64); err == nil && val > 0 {
			dailyLimit = val
		}
	}

	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	if webhookSecret == "" {
		webhookSecret = "topup_mock_secret_key_12345"
	}

	rzpKeyID := os.Getenv("RAZORPAY_KEY_ID")
	rzpKeySecret := os.Getenv("RAZORPAY_KEY_SECRET")
	rzpWebhookSecret := os.Getenv("RAZORPAY_WEBHOOK_SECRET")

	paymentProvider := os.Getenv("PAYMENT_PROVIDER")
	if paymentProvider == "" {
		if rzpKeyID != "" && rzpKeySecret != "" {
			paymentProvider = "RAZORPAY"
		} else {
			paymentProvider = "MOCK"
		}
	}

	// Environment mode validation: in production, fail closed if credentials/secrets are missing or mock
	if platformconfig.IsProduction() {
		if paymentProvider != "RAZORPAY" {
			panic("FATAL: In production mode, PAYMENT_PROVIDER must be configured to RAZORPAY")
		}
		if rzpKeyID == "" || rzpKeySecret == "" {
			panic("FATAL: In production mode, valid RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET are strictly required")
		}
		if rzpWebhookSecret == "" || rzpWebhookSecret == "topup_mock_secret_key_12345" {
			panic("FATAL: In production mode, a secure non-default RAZORPAY_WEBHOOK_SECRET is strictly required")
		}
	} else {
		// In development/test, if RAZORPAY_WEBHOOK_SECRET is unset, fall back to webhookSecret
		if rzpWebhookSecret == "" {
			rzpWebhookSecret = webhookSecret
		}
	}

	return &Config{
		Port:                  port,
		PostgresDSN:           postgresDSN,
		WalletGRPCAddr:        walletGRPCAddr,
		DailyLimitINR:         dailyLimit,
		WebhookSecret:         webhookSecret,
		PaymentProvider:       paymentProvider,
		RazorpayKeyID:         rzpKeyID,
		RazorpayKeySecret:     rzpKeySecret,
		RazorpayWebhookSecret: rzpWebhookSecret,
		ReconcilerInterval:    1 * time.Second,
		ExpiryInterval:        30 * time.Second,
	}
}
