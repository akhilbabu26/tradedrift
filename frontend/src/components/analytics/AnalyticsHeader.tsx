import { useState, useRef, useEffect } from 'react'
import { Calendar, ChevronDown, Check } from 'lucide-react'
import type { DateRangeOption } from '../../types/analytics'

interface Props {
  selectedRange?: DateRangeOption
  onRangeChange?: (range: DateRangeOption) => void
}

const DATE_OPTIONS: DateRangeOption[] = [
  'Last 7 Days',
  'Last 30 Days',
  'Last 90 Days',
  'Year to Date',
]

/**
 * Analytics page header matching the reference screenshot:
 * - Title: "Analytics & Journal"
 * - Subtitle
 * - Right side: Quote + Date range selector (default: "Last 30 Days")
 */
export default function AnalyticsHeader({
  selectedRange = 'Last 30 Days',
  onRangeChange,
}: Props) {
  const [open, setOpen] = useState(false)
  const [currentRange, setCurrentRange] = useState<DateRangeOption>(selectedRange)
  const dropdownRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const handleSelect = (option: DateRangeOption) => {
    setCurrentRange(option)
    setOpen(false)
    onRangeChange?.(option)
  }

  return (
    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 select-none">
      {/* Title & Subtitle */}
      <div>
        <h1 className="text-2xl font-bold text-[#f5f7fa] tracking-tight">
          Analytics &amp; Journal
        </h1>
        <p className="text-xs text-[#94a3b8] mt-1">
          Track your performance, analyze your trading activity, and improve your skills.
        </p>
      </div>

      {/* Right side: Quote + Date Range Dropdown */}
      <div className="flex flex-col sm:items-end gap-1.5 self-start sm:self-auto">
        <p className="text-[11px] text-[#64748b] italic hidden md:block">
          &ldquo;Discipline today, better trades tomorrow.&rdquo;
        </p>

        <div className="relative" ref={dropdownRef}>
          <button
            type="button"
            onClick={() => setOpen((prev) => !prev)}
            className="flex items-center gap-2 px-3.5 py-1.5 bg-[#111318] border border-[#1e2530] hover:border-[#334155] text-[#f5f7fa] text-xs font-medium rounded-lg transition-colors focus:outline-none focus:border-[#10b981]/50"
            aria-haspopup="listbox"
            aria-expanded={open}
          >
            <Calendar className="w-3.5 h-3.5 text-[#94a3b8]" />
            <span>{currentRange}</span>
            <ChevronDown className={`w-3.5 h-3.5 text-[#94a3b8] transition-transform duration-200 ${open ? 'rotate-180' : ''}`} />
          </button>

          {open && (
            <div className="absolute right-0 mt-1.5 w-40 bg-[#111318] border border-[#1e2530] rounded-lg shadow-xl shadow-black/60 py-1 z-30">
              {DATE_OPTIONS.map((opt) => {
                const isSelected = opt === currentRange
                return (
                  <button
                    key={opt}
                    type="button"
                    onClick={() => handleSelect(opt)}
                    className={`w-full flex items-center justify-between px-3 py-1.5 text-xs text-left transition-colors ${
                      isSelected
                        ? 'text-[#10b981] bg-[#10b981]/10 font-medium'
                        : 'text-[#94a3b8] hover:text-[#f5f7fa] hover:bg-[#1e2530]'
                    }`}
                  >
                    <span>{opt}</span>
                    {isSelected && <Check className="w-3.5 h-3.5 text-[#10b981]" />}
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
