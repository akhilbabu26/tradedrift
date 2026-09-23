// ─── Decimal.js Wrapper ──────────────────────────────────────────────────────
// All financial arithmetic in TradeDrift must go through this module.
// Never use raw JS number arithmetic for price, quantity, PnL, or balance.

import Decimal from 'decimal.js'

// Configure global Decimal settings
Decimal.set({ precision: 20, rounding: Decimal.ROUND_HALF_UP })

export { Decimal }

export const toDecimal = (value: string | number | Decimal): Decimal => new Decimal(value)

export const add = (a: string | number | Decimal, b: string | number | Decimal): Decimal =>
  new Decimal(a).plus(new Decimal(b))

export const subtract = (a: string | number | Decimal, b: string | number | Decimal): Decimal =>
  new Decimal(a).minus(new Decimal(b))

export const multiply = (a: string | number | Decimal, b: string | number | Decimal): Decimal =>
  new Decimal(a).times(new Decimal(b))

export const divide = (a: string | number | Decimal, b: string | number | Decimal): Decimal =>
  new Decimal(a).dividedBy(new Decimal(b))

export const percentage = (value: string | number | Decimal, total: string | number | Decimal): Decimal =>
  new Decimal(value).dividedBy(new Decimal(total)).times(100)
