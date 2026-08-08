# TradeDrift Frontend — Store & Global State Architecture Guide

> 📖 **Educational Reference**: This document details the client-side state management architecture, tools & libraries used, state hydration flows, and file responsibilities inside `src/store/`.

---

## 🎯 1. Purpose of the `src/store/` Folder

In React applications, **State** represents data that changes over time and drives what is rendered on the screen.

While local component state (`useState`) handles component-specific UI toggles (such as modal visibility or form input fields), **Global State (`src/store/`)**:
- Holds application-wide data that must be accessible across multiple pages and components without "prop drilling".
- Manages user authentication sessions (`accessToken`, `refreshToken`, `user` profile, `isAuthenticated`).
- Synchronizes in-memory reactive state with browser **`localStorage`** for persistent login across page refreshes.

---

## 🛠️ 2. Tools & Libraries Used in Store

| Tool / Library | Role & Usage |
| :--- | :--- |
| **Zustand (`zustand`)** | A fast, lightweight, and unopinionated state-management library built on hooks. It replaces verbose Redux boilerplate with simple `create()` store functions. |
| **TypeScript (`type User`)** | Imports the canonical `User` interface from `src/api/auth.ts` (`userId`, `email`, `username`), ensuring contract consistency between API responses and store state. |
| **HTML5 Web Storage (`localStorage`)** | Persists `access_token`, `refresh_token`, and JSON-serialized `user` data across browser reloads and tab closes. |
| **Zustand State Selectors** | Fine-grained reactive subscriptions in React components (e.g. `useAuthStore((s) => s.isAuthenticated)`) preventing unnecessary re-renders. |

---

## ⭐ 3. Features & Store Capabilities

- **🔐 Reactive Authentication State**: Holds `user`, `accessToken`, `refreshToken`, and `isAuthenticated` flag in memory.
- **💾 Session Token Persistence (`setTokens`)**: Atomically updates in-memory tokens while saving `access_token` and `refresh_token` into `localStorage`.
- **👤 User Profile Synchronization (`setUser`)**: Updates global user profile data in memory and serializes it to `localStorage.setItem('user', JSON.stringify(user))`.
- **🔄 Session Hydration (`hydrate`)**: Automatically called on app launch (`App.tsx`) to read stored tokens from `localStorage` and re-authenticate the user without requiring a re-login.
- **🧹 Clean Logout (`logout`)**: Instantly purges `access_token`, `refresh_token`, and `user` from both `localStorage` and reactive memory state.

---

## 📂 4. File-by-File Breakdown & Purpose

```
src/store/
└── authStore.ts   # Zustand authentication & user session state store
```

### Detailed File Description

#### 🔹 [authStore.ts](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/store/authStore.ts)
- **Purpose**: Defines the global authentication state store (`useAuthStore`).
- **Interfaces**:
  - `AuthState`: Specifies state properties (`user: User | null`, `accessToken: string | null`, `refreshToken: string | null`, `isAuthenticated: boolean`) and action methods (`setTokens`, `setUser`, `logout`, `hydrate`).
- **Store Actions**:
  - `setTokens(accessToken, refreshToken)`: Saves tokens to `localStorage` and sets `isAuthenticated = true`.
  - `setUser(user)`: Saves `User` object to `localStorage` as JSON and updates state.
  - `logout()`: Purges `localStorage` items and resets state back to `null` / `false`.
  - `hydrate()`: Reads `localStorage` on initial app render to restore existing login sessions.

---

## 🔄 5. State Synchronization Data Flow

```
   ┌───────────────────────────┐
   │    Backend Gateway API    │
   └─────────────┬─────────────┘
                 │
                 │ 1. HTTP Response (Tokens + UserDTO)
                 ▼
   ┌───────────────────────────┐
   │     src/api/auth.ts       │
   └─────────────┬─────────────┘
                 │
                 │ 2. Data returned to Page/Component
                 ▼
   ┌───────────────────────────┐
   │ LoginPage / RegisterPage  │
   └─────────────┬─────────────┘
                 │
                 │ 3. Calls setTokens() & setUser()
                 ▼
   ┌────────────────────────────────────────────────────────┐
   │              src/store/authStore.ts                    │
   │                                                        │
   │  ┌────────────────────────┐  ┌──────────────────────┐  │
   │  │   In-Memory State      │  │     localStorage     │  │
   │  │  - user                │  │  - access_token      │  │
   │  │  - isAuthenticated: true│  │  - refresh_token     │  │
   │  └──────────┬─────────────┘  └──────────────────────┘  │
   └─────────────┼──────────────────────────────────────────┘
                 │
                 │ 4. Reactive State Subscription
                 ▼
   ┌────────────────────────────────────────────────────────┐
   │                    React UI Views                      │
   │  - App.tsx (ProtectedRoute routing)                    │
   │  - Sidebar.tsx (User profile & logout trigger)         │
   │  - DashboardPage.tsx (Authenticated trade controls)    │
   └────────────────────────────────────────────────────────┘
```
