package mail

import (
	"context"
	"log"
)

// Mailer defines the contract for sending transactional emails.
type Mailer interface {
	SendVerificationCode(ctx context.Context, email string, code string) error
	SendPasswordResetCode(ctx context.Context, email string, code string) error
}

// LogMailer implements Mailer by printing emails to application logs.
type LogMailer struct{}

// NewLogMailer creates a new LogMailer.
func NewLogMailer() *LogMailer {
	return &LogMailer{}
}

func (m *LogMailer) SendVerificationCode(ctx context.Context, email string, code string) error {
	log.Printf("[SIMULATED EMAIL] ✉️ Sender: noreply@tradedrift.com | Recipient: %s\n"+
		"Subject: Verify your TradeDrift Account\n"+
		"Body: Welcome to TradeDrift! Your verification OTP code is: %s\n"+
		"Note: This code expires in 5 minutes.\n", email, code)
	return nil
}

func (m *LogMailer) SendPasswordResetCode(ctx context.Context, email string, code string) error {
	log.Printf("[SIMULATED EMAIL] ✉️ Sender: security@tradedrift.com | Recipient: %s\n"+
		"Subject: Reset your TradeDrift Password\n"+
		"Body: We received a request to reset your password. Your password reset OTP is: %s\n"+
		"Note: This code expires in 5 minutes.\n", email, code)
	return nil
}
