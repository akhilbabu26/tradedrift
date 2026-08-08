# TradeDrift Frontend — API Architecture & Services Guide

> 📖 **Educational Reference**: This document serves as a guide for developers learning how API request management, state synchronization, and TypeScript type safety work together in modern React applications.

---

## 🎯 1. Purpose of the `src/api/` Folder

In modern frontend web development, UI components (such as buttons, forms, and pages) should **never** directly handle raw HTTP network requests or hardcode backend API endpoints.

The `src/api/` folder provides a **Centralized API Abstraction Layer**. It sits between the user interface and the backend REST API server (`http://localhost:8080`).

### Why use a centralized API layer?
* **Separation of Concerns**: Components focus on presentation and user interaction; the API layer handles data fetching and HTTP protocol details.
* **Code Reusability**: Single-line function calls (e.g., `authApi.login(...)`) replace repetitive `fetch` or `axios` boilerplate.
* **Global Error & Token Management**: Interceptors automatically handle authorization headers and token refresh routines globally across the application.
* **Single Source of Truth**: Backend URL changes or header updates require changes in only one file ([client.ts](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/client.ts)).

---

## 📁 2. File Breakdown & Responsibilities

```
frontend/src/api/
├── client.ts   # Core HTTP client setup & interceptors
├── auth.ts     # Authentication domain DTOs & endpoints
└── wallet.ts   # Wallet & balance domain DTOs & endpoints
```

### 🔹 [client.ts](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/client.ts) — The Base HTTP Client
This file creates and exports a customized **Axios instance** (`client`) configured for TradeDrift.

**Key Responsibilities:**
1. **Dynamic Environment Configuration**: Reads `import.meta.env.VITE_API_BASE_URL` (defaulting to `http://localhost:8080`).
2. **Request Interceptor**: Automatically reads `access_token` from `localStorage` and attaches `Authorization: Bearer <token>` to every outgoing request header.
3. **Response Interceptor (Silent Token Refresh)**:
   - Listens for HTTP `401 Unauthorized` responses.
   - Automatically sends a request to `/api/v1/auth/refresh` with the saved `refreshToken`.
   - If token rotation succeeds, saves the new access token and retries the original request seamlessly.
   - If token refresh fails, clears `localStorage` and redirects the user to `/login`.

---

### 🔹 [auth.ts](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/auth.ts) — Authentication Service
Contains TypeScript interfaces for request/response payloads (Data Transfer Objects - DTOs) and exports the `authApi` service object.

**Key DTOs Defined:**
- `User`: `{ userId, email, username }`
- `LoginRequest` & `LoginResponse`: Login credentials and returned session tokens.
- `RegisterRequest` & `RegisterResponse`: Registration data and verification flags.

**Endpoints Covered:**
- `login`, `register`, `verifyEmail`, `resendVerification`
- `forgotPassword`, `resetPassword`, `changePassword`
- `logout`, `logoutAll`, `refresh`

---

### 🔹 [wallet.ts](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/wallet.ts) — Wallet & Asset Service
Manages account balance requests and asset configurations.

**Key DTOs Defined:**
- `Balance`: `{ asset, availableBalance, reservedBalance }`
- `AssetInfo`: Asset metadata, decimal precision, and seed amounts.

**Endpoints Covered:**
- `getAllBalances()`: Fetches all account asset balances for the logged-in user (`GET /api/v1/wallet/balances`).
- `getBalance(asset)`: Fetches single asset balance (`GET /api/v1/wallet/balances/{asset}`).
- `getSupportedAssets()`: Fetches list of platform supported trading pairs/assets (`GET /api/v1/wallet/assets`).

---

## 🛠️ 3. Core Technologies & Conceptual Architecture

### 1. TypeScript (Contract Enforcement & Type Safety)
TypeScript interfaces mirror backend DTOs (Data Transfer Objects). 
* **Benefit**: If a backend API returns `{ userId: string }` instead of `{ id: string }`, TypeScript prevents property misnaming bugs during compile time before code ever runs in the browser.

### 2. Axios (HTTP Library)
Axios is used over browser native `fetch()` because it supports:
* **Instances**: Creating pre-configured clients with base URLs and headers.
* **Interceptors**: Middleware functions that hook into requests before they leave and responses before they resolve.
* **Automatic JSON Transformation**: Automatically stringifies request payloads and parses response JSON.

### 3. Zustand (Global Client State Management)
While `src/api/` fetches data from the backend server, **[Zustand](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/store/authStore.ts)** manages client-side state in memory.

**How They Work Together:**
1. A component (e.g. `LoginPage.tsx`) calls `authApi.login(...)`.
2. The response data (tokens and user object) is received from `src/api/auth.ts`.
3. The component passes data to `useAuthStore.getState().setTokens(...)` and `setUser(...)`.
4. Zustand updates global state and syncs tokens to `localStorage`.
5. React UI components automatically re-render with updated user state.

---

## 🔄 4. Data Flow Diagram

```
[ UI Component (e.g., LoginPage) ]
               │
               ▼  1. Calls authApi.login(credentials)
     [ src/api/auth.ts ]
               │
               ▼  2. Uses configured client instance
    [ src/api/client.ts ]
               │  - Injects Authorization Header
               ▼
   [ Backend Gateway (Port 8080) ]
               │
               ▼  3. Returns JSON TokenPair + UserDTO
    [ src/api/client.ts ]
               │  - (If 401: Triggers refresh flow)
               ▼
[ UI Component receives Response Data ]
               │
               ▼  4. Updates Zustand Store (authStore.ts)
     [ React State Updated ] ➔ UI Re-renders ✨
```
