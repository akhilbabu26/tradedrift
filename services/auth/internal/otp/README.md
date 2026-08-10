# Auth Service — OTP Generator Package (`internal/otp`)

> **Package:** `tradedrift/services/auth/internal/otp`  
> **Directory:** `services/auth/internal/otp/`  
> **Role:** Secure One-Time Password (OTP) Generation & Cryptographic Hashing

---

## 1. Purpose & Responsibilities

The `otp` package generates cryptographically secure 6-digit verification codes and hashes them before persistence in PostgreSQL. It ensures verification codes cannot be brute-forced or retrieved in plaintext from database backups.
