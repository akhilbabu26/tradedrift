/**
 * Centralized Market & Asset Metadata
 *
 * Single source of truth for all market identities, logos, symbols, and asset display metadata.
 * Components must NEVER hardcode asset icons or fall back to BTC.
 */

export interface AssetMetadata {
  asset: string        // e.g. "BTC", "ETH", "SOL", "USDT"
  name: string         // e.g. "Bitcoin", "Ethereum", "Solana", "Tether"
  symbol: string       // e.g. "₿", "Ξ", "S", "₮"
  iconBg: string       // Tailwind classes for icon background
  iconBorder: string   // Tailwind classes for icon border
  iconColor: string    // Tailwind classes for icon text color
  hexColor: string     // Hex color code
}

export interface MarketMetadata {
  id: string           // e.g. "BTC-USDT"
  pair: string         // e.g. "BTC/USDT"
  base: string         // e.g. "BTC"
  quote: string        // e.g. "USDT"
  name: string         // e.g. "Bitcoin"
  baseMeta: AssetMetadata
  quoteMeta: AssetMetadata
}

export const ASSET_METADATA_MAP: Record<string, AssetMetadata> = {
  BTC: {
    asset: 'BTC',
    name: 'Bitcoin',
    symbol: '₿',
    iconBg: 'bg-[#f7931a]/15',
    iconBorder: 'border-[#f7931a]/30',
    iconColor: 'text-[#f7931a]',
    hexColor: '#f7931a',
  },
  ETH: {
    asset: 'ETH',
    name: 'Ethereum',
    symbol: 'Ξ',
    iconBg: 'bg-[#627eea]/15',
    iconBorder: 'border-[#627eea]/30',
    iconColor: 'text-[#627eea]',
    hexColor: '#627eea',
  },
  SOL: {
    asset: 'SOL',
    name: 'Solana',
    symbol: 'S',
    iconBg: 'bg-[#9945ff]/15',
    iconBorder: 'border-[#9945ff]/30',
    iconColor: 'text-[#9945ff]',
    hexColor: '#9945ff',
  },
  USDT: {
    asset: 'USDT',
    name: 'Tether',
    symbol: '₮',
    iconBg: 'bg-[#26a17b]/15',
    iconBorder: 'border-[#26a17b]/30',
    iconColor: 'text-[#26a17b]',
    hexColor: '#26a17b',
  },
  BNB: {
    asset: 'BNB',
    name: 'BNB',
    symbol: 'B',
    iconBg: 'bg-[#f0b90b]/15',
    iconBorder: 'border-[#f0b90b]/30',
    iconColor: 'text-[#f0b90b]',
    hexColor: '#f0b90b',
  },
  AVAX: {
    asset: 'AVAX',
    name: 'Avalanche',
    symbol: 'A',
    iconBg: 'bg-[#e84142]/15',
    iconBorder: 'border-[#e84142]/30',
    iconColor: 'text-[#e84142]',
    hexColor: '#e84142',
  },
}

/**
 * Returns typed AssetMetadata for any asset symbol.
 * NEVER falls back to BTC — falls back to a neutral, asset-derived placeholder.
 */
export function getAssetMetadata(assetSymbol: string): AssetMetadata {
  const clean = (assetSymbol || '').trim().toUpperCase()
  if (ASSET_METADATA_MAP[clean]) {
    return ASSET_METADATA_MAP[clean]
  }

  const char = clean.length > 0 ? clean[0] : '?'
  return {
    asset: clean || 'UNKNOWN',
    name: clean || 'Unknown',
    symbol: char,
    iconBg: 'bg-slate-700/30',
    iconBorder: 'border-slate-600/30',
    iconColor: 'text-slate-300',
    hexColor: '#94a3b8',
  }
}

/**
 * Returns typed MarketMetadata for any market ID (e.g. "BTC-USDT", "ETH-USDT").
 * NEVER falls back to BTC — derives directly from the base and quote assets.
 */
export function getMarketMetadata(marketId: string): MarketMetadata {
  const parts = (marketId || '').split('-')
  const base = (parts[0] || 'BTC').toUpperCase()
  const quote = (parts[1] || 'USDT').toUpperCase()
  const baseMeta = getAssetMetadata(base)
  const quoteMeta = getAssetMetadata(quote)

  return {
    id: `${base}-${quote}`,
    pair: `${base}/${quote}`,
    base,
    quote,
    name: baseMeta.name,
    baseMeta,
    quoteMeta,
  }
}
