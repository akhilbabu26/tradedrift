package config

import (
	"os"
	"strconv"
	"time"
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

	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		postgresDSN = "postgres://postgres:postgres@localhost:5432/tradedrift_topup?sslmode=disable"
	}

	walletGRPCAddr := os.Getenv("WALLET_GRPC_ADDR")
	if walletGRPCAddr == "" {
		walletGRPCAddr = "localhost:50052"
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
	if rzpWebhookSecret == "" {
		rzpWebhookSecret = webhookSecret
	}

	paymentProvider := os.Getenv("PAYMENT_PROVIDER")
	if paymentProvider == "" {
		if rzpKeyID != "" && rzpKeySecret != "" {
			paymentProvider = "RAZORPAY"
		} else {
			paymentProvider = "MOCK"
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
