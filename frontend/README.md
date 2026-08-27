# TradeDrift Pro — Frontend Trading Terminal

The official frontend web application for the **TradeDrift Cryptocurrency Exchange**, built with React 19, TypeScript, Vite, and Tailwind CSS.

---

## 📚 Documentation

For a detailed breakdown of the complete technology stack, architectural decisions, and why specialized trading libraries are used, refer to:

👉 **[`docs/TECH_STACK_AND_TOOLS.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/docs/TECH_STACK_AND_TOOLS.md)**

---

## ⚡ Tech Stack Summary

| Technology | Role |
| :--- | :--- |
| **React 19 + TypeScript** | Reactive component UI and strict type safety |
| **Vite 8** | High-speed HMR development and optimized production builds |
| **Tailwind CSS v3** | Institutional dark-mode styling and custom design tokens |
| **Zustand 5** | High-performance slice-based global state management |
| **Lightweight Charts (TradingView)** | 60fps canvas-rendered candlestick and volume charts |
| **Framer Motion** | Real-time price flash animations and smooth modal transitions |
| **Zod** | Pre-flight order submission and wallet address validation |
| **Radix UI Primitives** | Accessible balance sliders, modals, tabs, and tooltips |
| **Decimal.js** | Exact financial math eliminating JavaScript floating-point errors |
| **Axios + WebSockets** | Unified REST APIs and multi-market real-time data streaming |

---

## 🚀 Getting Started

### 1. Install Dependencies
```bash
npm install
npm install lightweight-charts framer-motion zod @radix-ui/react-slider @radix-ui/react-dialog @radix-ui/react-tabs @radix-ui/react-tooltip decimal.js date-fns clsx tailwind-merge
```

### 2. Start Development Server
```bash
npm run dev
```

### 3. Build for Production
```bash
npm run build
```

