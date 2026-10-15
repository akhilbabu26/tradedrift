import React, { type ReactNode, type HTMLAttributes } from 'react'

/**
 * TradeDrift Shared 3D Surface Design System Tokens
 *
 * Provides a unified monochrome material language matching the approved 3D sidebar:
 * - Pure dark canvas: #090B0D
 * - Raised surfaces: layered charcoal gradients with specular top highlights and soft ambient depth
 * - Flat surfaces: clean #111316 charcoal with #242629 border
 * - Inset surfaces: recessed #0A0B0E container for inputs, tabs, and nested wells
 * - Borders: #242629 with subtle white specular opacity
 * - Text: Primary #F5F5F5, Secondary #A7ABB2, Muted #777D85
 * - Accents: Emerald (#10B981) and Cyan (#06B6D4) for status and focus
 */
export const surfaceTokens = {
  colors: {
    canvas: '#08090B',
    sidebar: '#090A0C',
    card: '#151719',
    raised: '#202225',
    raisedTop: '#24272D',
    raisedMid: '#1A1C20',
    raisedBottom: '#121417',
    flat: '#151719',
    inset: '#090A0C',
    border: '#292C30',
    borderSubtle: 'rgba(255, 255, 255, 0.07)',
    borderHighlight: 'rgba(255, 255, 255, 0.16)',
    textPrimary: '#F2F3F5',
    textSecondary: '#A7ADB5',
    textMuted: '#777E87',
    monochromeHighlight: '#D9DCE0',
    accentEmerald: '#10B981',
    accentRed: '#EF4444',
  },
  gradients: {
    raised: 'linear-gradient(180deg, #22252A 0%, #17191D 48%, #111316 100%)',
    raisedSubtle: 'linear-gradient(180deg, #1B1D22 0%, #131518 100%)',
    flat: '#151719',
    inset: '#090A0C',
  },
  shadows: {
    raised:
      '0 12px 28px -6px rgba(0, 0, 0, 0.7), 0 3px 8px rgba(0, 0, 0, 0.45), inset 0 1px 0 rgba(255, 255, 255, 0.12), inset 0 -1px 0 rgba(0, 0, 0, 0.35)',
    raisedSubtle:
      '0 8px 20px -4px rgba(0, 0, 0, 0.55), 0 2px 4px rgba(0, 0, 0, 0.35), inset 0 1px 0 rgba(255, 255, 255, 0.08)',
    flat: '0 2px 6px rgba(0, 0, 0, 0.3)',
    inset: 'inset 0 2px 4px rgba(0, 0, 0, 0.6), inset 0 0 0 1px rgba(0, 0, 0, 0.2)',
  },
  radii: {
    sm: '0.375rem', // 6px
    md: '0.5rem', // 8px
    lg: '0.75rem', // 12px
    xl: '1rem', // 16px
  },
  transitions: {
    standard: 'all 150ms cubic-bezier(0.4, 0, 0.2, 1)',
  },
} as const

export type SurfaceVariant = 'raised' | 'raised-subtle' | 'flat' | 'inset'

export interface RaisedSurfaceProps extends HTMLAttributes<HTMLDivElement> {
  children?: ReactNode
  variant?: SurfaceVariant
  className?: string
  noPadding?: boolean
  elevated?: boolean
  interactive?: boolean
  as?: React.ElementType
}

/**
 * RaisedSurface / Panel
 *
 * Core reusable container implementing the approved 3D surface material.
 * Offers 'raised', 'raised-subtle', 'flat', and 'inset' variants.
 */
export function RaisedSurface({
  children,
  variant = 'raised',
  className = '',
  noPadding = false,
  elevated = false,
  interactive = false,
  as: Component = 'div',
  style,
  ...rest
}: RaisedSurfaceProps) {
  let surfaceStyle: React.CSSProperties = {}
  let baseClasses = 'transition-all duration-150'
  if (interactive) {
    baseClasses += ' hover:border-[rgba(255,255,255,0.12)] hover:border-t-[rgba(255,255,255,0.20)] hover:shadow-[0_12px_28px_-4px_rgba(0,0,0,0.65),0_4px_8px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.12)]'
  }

  if (variant === 'raised') {
    surfaceStyle = {
      background: surfaceTokens.gradients.raised,
      border: '1px solid rgba(255, 255, 255, 0.07)',
      borderTop: '1px solid rgba(255, 255, 255, 0.16)',
      boxShadow: elevated
        ? '0 16px 36px -8px rgba(0, 0, 0, 0.8), 0 4px 12px rgba(0, 0, 0, 0.5), inset 0 1px 0 rgba(255, 255, 255, 0.18)'
        : surfaceTokens.shadows.raised,
      ...style,
    }
    baseClasses += ' rounded-xl'
  } else if (variant === 'raised-subtle') {
    surfaceStyle = {
      background: surfaceTokens.gradients.raisedSubtle,
      border: '1px solid rgba(255, 255, 255, 0.06)',
      borderTop: '1px solid rgba(255, 255, 255, 0.11)',
      boxShadow: surfaceTokens.shadows.raisedSubtle,
      ...style,
    }
    baseClasses += ' rounded-xl'
  } else if (variant === 'inset') {
    surfaceStyle = {
      backgroundColor: surfaceTokens.colors.inset,
      border: `1px solid ${surfaceTokens.colors.border}`,
      boxShadow: surfaceTokens.shadows.inset,
      ...style,
    }
    baseClasses += ' rounded-lg'
  } else {
    // 'flat'
    surfaceStyle = {
      backgroundColor: surfaceTokens.colors.flat,
      border: `1px solid ${surfaceTokens.colors.border}`,
      boxShadow: surfaceTokens.shadows.flat,
      ...style,
    }
    baseClasses += ' rounded-xl'
  }

  const paddingClass = noPadding ? '' : 'p-4 sm:p-5'

  return (
    <Component
      className={`${baseClasses} ${paddingClass} ${className}`}
      style={surfaceStyle}
      {...rest}
    >
      {children}
    </Component>
  )
}

// Alias for convenience
export const Panel = RaisedSurface

export interface SurfaceHeaderProps {
  title: ReactNode
  subtitle?: ReactNode
  action?: ReactNode
  icon?: React.ComponentType<{ size?: number; className?: string }>
  className?: string
}

/**
 * SurfaceHeader
 *
 * Standardized header component for panels and raised surfaces.
 */
export function SurfaceHeader({
  title,
  subtitle,
  action,
  icon: Icon,
  className = '',
}: SurfaceHeaderProps) {
  return (
    <div className={`flex items-start justify-between flex-wrap gap-2.5 mb-4 ${className}`}>
      <div className="flex items-center gap-2.5 min-w-0">
        {Icon && (
          <div className="w-7 h-7 rounded-lg bg-[#14161B] border border-[#242629] flex items-center justify-center text-[#777D85] flex-shrink-0">
            <Icon size={14} className="text-[#A7ABB2]" />
          </div>
        )}
        <div className="flex flex-col min-w-0">
          <div className="text-[14px] font-semibold text-[#F5F5F5] tracking-tight truncate leading-tight">
            {title}
          </div>
          {subtitle && (
            <div className="text-[11px] text-[#777D85] mt-0.5 truncate leading-tight">
              {subtitle}
            </div>
          )}
        </div>
      </div>
      {action && <div className="flex items-center gap-2 flex-shrink-0">{action}</div>}
    </div>
  )
}

export interface SurfaceDividerProps {
  orientation?: 'horizontal' | 'vertical'
  className?: string
}

/**
 * SurfaceDivider
 *
 * Subtle separator matching the charcoal palette and sidebar dividers.
 */
export function SurfaceDivider({
  orientation = 'horizontal',
  className = '',
}: SurfaceDividerProps) {
  if (orientation === 'vertical') {
    return (
      <div
        className={`w-px bg-[#242629]/70 flex-shrink-0 self-stretch ${className}`}
        aria-hidden="true"
      />
    )
  }
  return (
    <div
      className={`h-px w-full bg-[#242629]/70 my-3 flex-shrink-0 ${className}`}
      aria-hidden="true"
    />
  )
}

export interface SurfaceBadgeProps {
  children: ReactNode
  variant?: 'emerald' | 'cyan' | 'neutral' | 'amber' | 'red'
  className?: string
}

/**
 * SurfaceBadge
 *
 * Understated status and tag badge for raised panels.
 */
export function SurfaceBadge({
  children,
  variant = 'neutral',
  className = '',
}: SurfaceBadgeProps) {
  const variantStyles = {
    emerald: 'bg-[#10B981]/10 border-[#10B981]/25 text-[#10B981]',
    cyan: 'bg-[#06B6D4]/10 border-[#06B6D4]/25 text-[#06B6D4]',
    neutral: 'bg-[#181A1F] border-[#242629] text-[#A7ABB2]',
    amber: 'bg-[#F59E0B]/10 border-[#F59E0B]/25 text-[#F59E0B]',
    red: 'bg-[#EF4444]/10 border-[#EF4444]/25 text-[#EF4444]',
  }

  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full border text-[11px] font-medium tracking-wide ${variantStyles[variant]} ${className}`}
    >
      {children}
    </span>
  )
}

export default RaisedSurface
