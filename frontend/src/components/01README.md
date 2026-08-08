# TradeDrift Frontend — Components Architecture & Guide

> 📖 **Educational Reference**: This document details the component architecture, UI design system, tools & libraries used, and individual file responsibilities across the TradeDrift frontend.

---

## 🎯 1. Purpose of the `src/components/` Folder

In modern React architecture, **Components** are modular, self-contained, and reusable UI building blocks.

Unlike **Pages** (which manage top-level routes and full-page layout assembly), **Components** focus on:
- **Reusability**: Rendering consistent UI elements (Navbar, Footer, Tickers, Cards) across multiple views.
- **Isolation**: Encapsulating styling, animations, icons, and visual presentation logic in single files.
- **Single Responsibility Principle**: Each component handles one distinct visual section of the interface.

---

## 🛠️ 2. Tools & Libraries Used in Components

| Tool / Library | Role & Usage |
| :--- | :--- |
| **React (TypeScript / TSX)** | Component rendering, functional hooks (`useState`, `useEffect`, `useRef`), typed props. |
| **TailwindCSS & Custom CSS** | Dark mode utility classes (`bg-[#0a0b0e]`, `border-[#1f2229]`), glassmorphism (`backdrop-blur-xl`), and green glow shadows (`glow-green`). |
| **Lucide React (`lucide-react`)** | Lightweight, scalable vector icons (`Mail`, `Lock`, `ShieldCheck`, `Eye`, `EyeOff`, `Gem`, `LogOut`, `BarChart2`, `Wallet`, etc.). |
| **HTML5 Canvas 2D API** | Custom particle animation math, physics velocity calculation, particle connections, and responsive resize rendering in `WebGLBackground.tsx`. |
| **React Router DOM (`react-router-dom`)** | `Link`, `useLocation()`, and `useNavigate()` for SPA navigation, active link styling, and route redirection. |
| **Zustand & Axios Integration** | State management reset (`useAuthStore().logout()`) and API token revocation (`authApi.logout()`) inside `Sidebar.tsx`. |

---

## ⭐ 3. Component Features & UI Capabilities

- **✨ Interactive Particle Shaders**: `WebGLBackground.tsx` renders a dynamic 3D particle canvas field with smooth node connections and mouse interaction physics.
- **💎 Glassmorphism Design System**: Translucent dark surfaces (`bg-[#111318]/80`) with `backdrop-blur-xl` and crisp borders (`border-[#1f2229]`) create a modern crypto exchange aesthetic.
- **📈 Live Price Ticker Bar**: `TickerBar.tsx` displays scrolling market prices for top assets (BTC, ETH, SOL, AVAX) with price change indicators.
- **🖥️ Interactive Exchange Preview**: `DashboardPreview.tsx` renders a live-looking mock trading interface complete with orderbook depth, candlestick chart preview, and order form controls.
- **🧭 Route-Aware Navigation**: `Sidebar.tsx` reads current browser location to highlight active links, displays pro tier upgrade cards, and handles secure user sign-out.

---

## 📂 4. File-by-File Breakdown & Purpose

```
src/components/
├── AnimatedBackground.tsx # CSS gradient ambient mesh fallback
├── AuthCard.tsx           # Reusable glassmorphic wrapper card for auth forms
├── DashboardPreview.tsx   # Mock trading terminal preview on landing page
├── FeaturesSection.tsx    # Platform feature cards grid
├── Footer.tsx             # Global bottom navigation & legal disclaimers
├── HeroSection.tsx        # High-impact landing page hero header
├── HowItWorks.tsx         # 3-step platform workflow section
├── Navbar.tsx             # Public top header navigation bar
├── TickerBar.tsx          # Real-time animated crypto price ticker
├── WebGLBackground.tsx    # HTML5 Canvas 2D animated particle system
└── dashboard/
    └── Sidebar.tsx        # Protected app navigation sidebar drawer
```

### Detailed File Descriptions

#### 1. [Navbar.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/Navbar.tsx)
- **Purpose**: Global public navigation bar.
- **Features**: TradeDrift branding logo, public navigation links (`Markets`, `Trade`, `Features`, `About`), and CTAs (`Sign In`, `Get Started`).
- **Tools Used**: `React`, `Link` from `react-router-dom`, Lucide icons.

#### 2. [HeroSection.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/HeroSection.tsx)
- **Purpose**: The main visual hero banner on the landing page.
- **Features**: Bold gradient headline ("Trade Real Crypto Markets with Zero Risk"), live market badges, call-to-action buttons, and platform trust indicators.
- **Tools Used**: Tailwind flexbox/grid layout, custom typography classes, Lucide icons (`ArrowRight`, `ShieldCheck`).

#### 3. [TickerBar.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/TickerBar.tsx)
- **Purpose**: Animated horizontal crypto price ticker bar.
- **Features**: Displays trading pairs (`BTC/USDT`, `ETH/USDT`, `SOL/USDT`, `AVAX/USDT`) with live prices, 24h percentage changes, and directional indicators.
- **Tools Used**: Tailwind flexbox, Lucide icons (`TrendingUp`, `TrendingDown`).

#### 4. [DashboardPreview.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/DashboardPreview.tsx)
- **Purpose**: Showcase demonstration component on the landing page.
- **Features**: Interactive tab controls (Chart, Orderbook, Orders) allowing prospective users to preview the trading dashboard before registering.
- **Tools Used**: React state (`useState`), Lucide icons (`BarChart2`, `Activity`, `Layers`).

#### 5. [FeaturesSection.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/FeaturesSection.tsx)
- **Purpose**: Grid highlighting platform technical strengths.
- **Features**: 6 feature cards detailing Price-Time Priority Matching, Virtual Wallet Provisioning, Real-time WebSockets, Audit Ledger, and Zero Risk Simulation.
- **Tools Used**: Tailwind grid layout (`grid-cols-1 md:grid-cols-3`), hover state transitions.

#### 6. [HowItWorks.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/HowItWorks.tsx)
- **Purpose**: Informational step-by-step onboarding guide.
- **Features**: 3 step-by-step cards: *1. Create Account*, *2. Get 10,000 USDT Virtual Funds*, *3. Execute Real Orders*.
- **Tools Used**: Responsive grid cards with emerald step badge indicators.

#### 7. [Footer.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/Footer.tsx)
- **Purpose**: Global footer area.
- **Features**: Logo, column navigation links (Platform, Resources, Legal), social media icons, copyright notice, and real-time backend operational status badge.
- **Tools Used**: Lucide icons (`Github`, `Twitter`, `Globe`, `Activity`), React Router `Link`.

#### 8. [WebGLBackground.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/WebGLBackground.tsx)
- **Purpose**: Full-screen interactive background animation.
- **Features**: Spawns floating particles that move with physics vectors, connects nearby particles with semi-transparent lines, and responds to window resize events.
- **Tools Used**: `useRef`, `useEffect`, HTML5 Canvas 2D Context API (`requestAnimationFrame`), math trigonometry.

#### 9. [AnimatedBackground.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/AnimatedBackground.tsx)
- **Purpose**: Lightweight CSS ambient background mesh.
- **Features**: Radial gradient glowing color orbs (`emerald-500/10`, `cyan-500/10`) for smooth performance on lower-spec GPUs.
- **Tools Used**: Pure CSS blur filters (`blur-[120px]`).

#### 10. [AuthCard.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/AuthCard.tsx)
- **Purpose**: Reusable form container card for authentication pages.
- **Features**: Provides glassmorphism border styling, backdrop blur, padding, and centered layout alignment for forms.
- **Tools Used**: React `children` prop pattern, Tailwind backdrop blur utilities.

#### 11. [dashboard/Sidebar.tsx](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/components/dashboard/Sidebar.tsx)
- **Purpose**: Left navigation sidebar for protected application views (`/dashboard`, `/wallet`, `/settings`).
- **Features**:
  - Highlights active menu items dynamically using `useLocation()`.
  - Pro tier upgrade promotional banner with dismiss option.
  - Integrated Sign-Out button calling `authApi.logout()` and clearing Zustand state (`useAuthStore().logout()`).
- **Tools Used**: `react-router-dom`, `useAuthStore` (Zustand), `authApi` (Axios service), `lucide-react` icons.

---

## 🎨 5. Color Palette & Styling Tokens

- **Background Base**: `#0a0b0e`
- **Surface Card**: `#111318`
- **Border Overlay**: `#1f2229`
- **Brand Green (Accent)**: `#10b981` (`emerald-500`)
- **Danger Red**: `#ef4444` (`red-500`)
- **Text Main**: `#ffffff`
- **Text Muted**: `#94a3b8` (`slate-400`)
