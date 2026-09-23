# Frontend Architecture & Tooling Rationale

**Project:** TradeDrift Cryptocurrency Exchange  
**Documentation:** `frontend/docs/TECH_STACK_AND_TOOLS.md`  
**Topic:** Frontend Tooling Guide, Technology Stack, and Package Rationale  
**Last Updated:** September 2026  

---

## 1. Executive Summary

The TradeDrift frontend is an institutional-grade cryptocurrency trading interface designed to handle **real-time Level-2 order book streaming**, **sub-second trade execution**, and **high-frequency market data visualization**.

Standard web application stacks often suffer from UI stutter, floating-point rounding errors, and DOM rendering lockup when exposed to live cryptocurrency feeds. This document outlines the **complete frontend technology stack**, the **3-tier state architecture**, and details **why each specialized tool is required**.

```
 ┌──────────────────────────────────────────────────────────────────────────────────┐
 │                        TRADEDRIFT FRONTEND TECH MATRIX                           │
 ├──────────────────────────────────────────────────────────────────────────────────┤
 │ ⚡ Core Engine:         React 19 + TypeScript + Vite 8                           │
 │ 🎨 Styling & Design:     Tailwind CSS v3 + Custom Dark Theme + Custom Tokens      │
 │ 🗃️ Client UI State:     Zustand 5 (Fast Form Inputs, Modals & Client Preferences) │
 │ 🔄 Server State:        TanStack Query v5 (Auto Caching, Deduplication, Sync)    │
 │ 🚦 Routing:             React Router DOM v7 (Protected Route Guards)             │
 │ 🌐 Network & REST:      Axios (Interceptors & Centralized Error Handling)        │
 │ ⚡ Real-Time Streaming: Native WebSockets (Level-2 Depth & Live Trade Tape)      │
 │ 📈 Charting:            Lightweight Charts (Canvas Engine by TradingView)        │
 │ ✨ Micro-Animations:    Framer Motion (Price Tick Flashes & Smooth Drawers)      │
 │ 🛡️ Form Validation:     Zod (Strict Input & Address Schemas)                     │
 │ 🧩 UI Primitives:       Radix UI / Shadcn (Accessible Sliders, Modals & Tabs)    │
 │ 💰 Precision Math:      Decimal.js (Zero Floating-Point Error Math)              │
 │ 🕒 Date Utilities:      Date-fns (Millisecond-Accurate History Formatting)       │
 │ 🔔 Feedback:            React Hot Toast (Dark-Themed Non-Blocking Alerts)        │
 │ 🔧 Class Utility:       clsx + tailwind-merge (Dynamic `cn()` Helper)            │
 └──────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. The 3-Tier State Separation Architecture

In a high-frequency trading platform, mixing high-frequency data, server state, and UI form state inside a single store causes massive rendering bottlenecks. TradeDrift enforces a strict **3-tier separation of concerns**:

```
                          TradeDrift Frontend State
                                     │
           ┌─────────────────────────┼─────────────────────────┐
           │                         │                         │
      Client State              Server State            Real-Time Feed
           │                         │                         │
        Zustand 5             TanStack Query v5            WebSocket
           │                         │                         │
     - Form Inputs             - User Profile            - Level-2 Order Book
     - Selected Pair           - Wallet Balances         - Live Trade Tape
     - Active Side (Buy/Sell)  - Open & Past Orders      - Real-Time Price Ticks
     - Modal & Drawer Toggles  - Portfolio Holdings            │
     - Tab Selections          - Market List                   │
                                     │                         │
                                REST / Axios                   │
                                     │                         │
                                     ▼                         ▼
                              Auto-Refetching           Canvas / Local Ref
                             Optimistic Updates         (Zero Global Lag)
```

### 2.1 Tier 1: Client UI State (`Zustand 5`)
* **Role:** Manages pure local user interface interactions that do not belong to the database.
* **Stores:**
  * `tradingUIStore`: Selected market (`BTC-USDT`), active side (`buy` / `sell`), order type (`limit` / `market`), input price, input quantity, slider percentage.
  * `authStore`: Active access token, user session hydration, client authentication status.

### 2.2 Tier 2: Server State (`TanStack Query v5`)
* **Role:** Manages all asynchronous data fetched over REST APIs from the TradeDrift microservices.
* **Benefits:**
  * Automatic caching and deduplication of redundant API calls.
  * Background revalidation (`staleTime: 5000`) and window focus refetching.
  * Direct cache invalidation when receiving matching engine events over WebSocket.
  * Automated `isLoading`, `isError`, and retry state handling without custom boilerplate.

### 2.3 Tier 3: High-Frequency Real-Time State (`Native WebSocket`)
* **Role:** Streams sub-second market data directly from the gateway without triggering full component tree re-renders.
* **Handling:** High-frequency Level-2 depth updates and trade tape ticks feed directly into optimized local components, canvas buffers, or refs to preserve 60 FPS fluidity.

---

## 3. Specialized Trading Tools & Rationale

---

### 3.1 📈 `lightweight-charts` (by TradingView)
* **Category:** High-Performance Financial Charting
* **Description:** High-performance Canvas-based financial charting optimized for large time-series datasets and real-time updates.
* **Why It Was Chosen:**  
  Standard SVG-based charting libraries (Recharts, Chart.js) choke when rendering multi-day candlestick charts. `lightweight-charts` utilizes a pure HTML5 Canvas engine, providing smooth navigation, crosshairs, timeframes (`1m` to `1D`), and price markers for active open orders without DOM bloat.
* **Primary Locations:** `src/features/trading/chart/`, `src/features/markets/`.

---

### 3.2 🔄 `@tanstack/react-query` (TanStack Query v5)
* **Category:** Asynchronous Server State & Cache Management
* **Why It Is Needed:**  
  Eliminates fragile `useEffect` data fetching loops and manual loading/error flags across portfolio, orders, wallet, and market views.
* **Why It Was Chosen:**  
  Provides declarative queries (`useQuery`), instant mutations with optimistic UI updates (`useMutation`), and seamless synchronization with WebSocket events.
* **Primary Locations:** `src/features/*/hooks/`, `src/services/api/`.

---

### 3.3 🗃️ `zustand` (v5)
* **Category:** Granular Client State Management
* **Why It Is Needed:**  
  Allows components (like the order form or market selector) to subscribe strictly to the exact state variables they need, preventing cascading re-renders across the page.
* **Primary Locations:** `src/stores/`.

---

### 3.4 💰 `decimal.js`
* **Category:** Arbitrary-Precision Financial Math
* **Why It Is Needed:**  
  Standard JavaScript numbers use IEEE 754 double-precision floats, which cause notorious precision bugs:
  ```javascript
  // JavaScript float bug:
  0.1 + 0.2 === 0.30000000000000004 // ❌ Corrupts balance calculations
  ```
* **Why It Was Chosen:**  
  Guarantees exact mathematical precision for:
  $$\text{Total Cost} = \text{Price} \times \text{Quantity} + \text{Fee}$$
  Prevents discrepancies between client-side previews and matching engine execution.
* **Primary Locations:** `src/lib/decimal.ts`, `src/features/trading/order-form/`.

---

### 3.5 ✨ `framer-motion`
* **Category:** Hardware-Accelerated Micro-Animations
* **Why It Is Needed:**  
  Provides instantaneous visual cues when markets move:
  1. **Price Flash Animations:** Immediate emerald green or coral red background flashes when best bid or ask changes.
  2. **Order Book Depth Bars:** Smooth transitions for horizontal liquidity depth bars.
  3. **Drawer & Modal Entrances:** Fluid dialog presentation.
* **Usage Rule:** Use sparingly to preserve GPU resources for charting and live data.
* **Primary Locations:** `src/features/trading/order-book/`, `src/components/ui/modal/`.

---

### 3.6 🛡️ `zod`
* **Category:** Type-Safe Schema Declaration & Validation
* **Why It Is Needed:**  
  Enforces pre-flight validation on order submissions before network dispatch:
  * `price > 0` and conforms to market tick increment.
  * `quantity > 0` and conforms to lot size bounds.
  * `estimated_cost <= available_balance`.
* **Primary Locations:** `src/schemas/`.

---

### 3.7 🧩 `@radix-ui/react-*` (Radix Primitives)
* **Category:** Accessible, Headless UI Primitives
* **Components Included:**
  * `@radix-ui/react-slider`: Balance allocation slider (`25%`, `50%`, `75%`, `100%`).
  * `@radix-ui/react-dialog`: Accessible modal/drawer overlays for Top-Up and confirmations.
  * `@radix-ui/react-tabs`: Zero-lag tab switcher for `Buy / Sell` and `Limit / Market`.
  * `@radix-ui/react-tooltip`: Informational tooltips for Maker vs. Taker fees and order flags.
* **Primary Locations:** `src/components/ui/`.

---

### 3.8 🕒 `date-fns`
* **Category:** Modular Date & Timestamp Formatting
* **Why It Is Needed:**  
  Tree-shakeable, millisecond-accurate timestamp formatting (`HH:mm:ss.SSS` for matching engine trade tape, `MMM dd, yyyy HH:mm` for order ledger).
* **Primary Locations:** `src/lib/formatting.ts`.

---

### 3.9 🎨 `clsx` & `tailwind-merge` (`cn` helper)
* **Category:** Dynamic Class Name Composition
* **Implementation (`src/lib/utils.ts`):**
  ```typescript
  import { clsx, type ClassValue } from 'clsx'
  import { twMerge } from 'tailwind-merge'

  export function cn(...inputs: ClassValue[]) {
    return twMerge(clsx(inputs))
  }
  ```

---

## 4. Feature-Driven Directory Architecture

TradeDrift follows a **Feature-Driven (Bulletproof React)** layout to ensure modularity and clean separation of concerns:

```text
src/
├── app/
│   ├── router/                 # React Router definitions & ProtectedRoute guards
│   ├── providers/              # QueryClientProvider, Toaster, Theme
│   └── App.tsx
│
├── features/                   # Self-contained business domains
│   ├── auth/                   # Login, Register, Verify OTP
│   ├── trading/                # Order desk (OrderForm, OrderBook, Chart, TradeTape)
│   ├── portfolio/              # Holdings breakdown, PnL, allocation ring
│   ├── wallet/                 # Balances, Fiat Top-Up (Razorpay), transaction ledger
│   ├── orders/                 # Open orders, Order history, Trade fills
│   ├── markets/                # Gainers/losers, Ticker table, Search & filters
│   ├── analytics/              # Performance scorecard, Asset PnL breakdown
│   └── settings/               # Profile, Password change, Session revocation
│
├── stores/                     # Zustand stores (Client UI state only)
│   ├── authStore.ts
│   ├── marketStore.ts
│   └── tradingUIStore.ts
│
├── services/                   # Network services
│   ├── api/                    # Axios instances & REST service endpoints
│   └── websocket/              # WebSocket client & market streaming handlers
│
├── components/                 # Shared / Reusable components
│   ├── ui/                     # Primitives (Button, Input, Modal, Slider, Tabs)
│   ├── layout/                 # Sidebar, TopBar, AppLayout shell
│   └── common/                 # EmptyState, ErrorBoundary, StatCard
│
├── schemas/                    # Zod validation schemas
├── types/                      # Shared TypeScript interfaces & DTO contracts
├── lib/                        # Math utilities (Decimal.js), formatting, cn()
└── main.tsx
```

---

## 5. Package Installation Reference

To install the complete validated suite of frontend tools:

```bash
npm install @tanstack/react-query lightweight-charts framer-motion zod @radix-ui/react-slider @radix-ui/react-dialog @radix-ui/react-tabs @radix-ui/react-tooltip decimal.js date-fns clsx tailwind-merge
```

```bash
npm install -D @types/decimal.js
```
