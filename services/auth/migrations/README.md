# Auth Service — Database Migrations (`migrations`)

> **Directory:** `services/auth/migrations/`  
> **Target Database:** `tradedrift_auth` (PostgreSQL)  
> **Tool:** Goose SQL Migrations

---

## 1. DDL Schema Summary

The migration scripts in this directory initialize the database tables for user identities, email verification states, hashed credentials, and refresh token revocation records.

### Applied Migration Scripts:
- [`00001_create_auth_tables.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/migrations/00001_create_auth_tables.sql): Creates `users`, `refresh_tokens`, and `verification_codes` tables with unique indexes on `email` and `username`.
