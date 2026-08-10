# Auth Service — Mailer Adapter (`internal/mail`)

> **Package:** `tradedrift/services/auth/internal/mail`  
> **Directory:** `services/auth/internal/mail/`  
> **Role:** Email Sending Adapter for Verification & Reset Codes

---

## 1. Purpose & Responsibilities

The `mail` package provides the email delivery adapter for sending verification codes and password reset tokens to users. In development mode, it logs email messages; in production mode, it integrates with SMTP / transactional email providers (SendGrid, Mailgun, AWS SES).
