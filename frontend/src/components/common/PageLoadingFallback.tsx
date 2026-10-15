export default function PageLoadingFallback() {
  return (
    <div
      role="status"
      aria-label="Loading page"
      className="flex-1 flex flex-col items-center justify-center min-h-[300px] w-full bg-[#08090B] gap-3 select-none"
    >
      <div className="w-5 h-5 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
      <span className="text-xs text-slate-500 font-mono">Loading...</span>
    </div>
  )
}
