import { useState } from 'react'
import { Download, Copy, ExternalLink, X, Check, Camera } from 'lucide-react'
import toast from 'react-hot-toast'
import { useModalFocus } from '../../hooks/useModalFocus'

interface ChartScreenshotModalProps {
  isOpen: boolean
  onClose: () => void
  dataUrl: string
  blob: Blob | null
  filename: string
  symbol: string
  timeframe: string
}

export default function ChartScreenshotModal({
  isOpen,
  onClose,
  dataUrl,
  blob,
  filename,
  symbol,
  timeframe,
}: ChartScreenshotModalProps) {
  const [copied, setCopied] = useState(false)
  const [saving, setSaving] = useState(false)
  const modalRef = useModalFocus<HTMLDivElement>({ isOpen, onClose })

  if (!isOpen) return null

  // 1. Save Image (.PNG) with native File System Access (Windows Save As dialog)
  const handleSave = async () => {
    setSaving(true)
    try {
      // Modern Chromium (Edge & Chrome on Windows): opens native Windows "Save As" dialog
      if ('showSaveFilePicker' in window && blob) {
        try {
          const handle = await (window as unknown as {
            showSaveFilePicker: (options: {
              suggestedName: string
              types: { description: string; accept: Record<string, string[]> }[]
            }) => Promise<{
              createWritable: () => Promise<{
                write: (data: Blob) => Promise<void>
                close: () => Promise<void>
              }>
            }>
          }).showSaveFilePicker({
            suggestedName: filename,
            types: [
              {
                description: 'PNG Image (*.png)',
                accept: { 'image/png': ['.png'] },
              },
            ],
          })

          const writable = await handle.createWritable()
          await writable.write(blob)
          await writable.close()
          toast.success(`Saved as ${filename}!`)
          setSaving(false)
          return
        } catch (err: unknown) {
          // If user aborted / cancelled dialog, do nothing
          if (err && typeof err === 'object' && 'name' in err && err.name === 'AbortError') {
            setSaving(false)
            return
          }
          console.warn('showSaveFilePicker fallback:', err)
        }
      }

      // Fallback: programmatic anchor download
      const url = blob ? URL.createObjectURL(blob) : dataUrl
      const link = document.createElement('a')
      link.download = filename
      link.setAttribute('download', filename)
      link.href = url
      link.rel = 'noopener'
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)

      if (blob) {
        setTimeout(() => URL.revokeObjectURL(url), 10000)
      }
      toast.success('Download started!')
    } catch (err) {
      console.error('Save failed:', err)
      toast.error('Failed to save file. Use "Copy Image" or "Open in Tab" instead.')
    } finally {
      setSaving(false)
    }
  }

  // 2. Copy image directly to Windows clipboard
  const handleCopy = async () => {
    if (!blob) {
      toast.error('Image data unavailable for copy')
      return
    }

    try {
      if (navigator.clipboard && window.ClipboardItem) {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
        setCopied(true)
        toast.success('Copied to clipboard! Press Ctrl+V to paste.')
        setTimeout(() => setCopied(false), 2500)
      } else {
        toast.error('Clipboard image copy not supported in this browser')
      }
    } catch (err) {
      console.error('Clipboard write error:', err)
      toast.error('Could not copy image. Check browser permissions.')
    }
  }

  // 3. Open image in a new tab for native browser inspection / saving
  const handleOpenInNewTab = () => {
    const win = window.open()
    if (win) {
      win.document.write(`
        <!DOCTYPE html>
        <html>
          <head>
            <title>${filename}</title>
            <style>
              body {
                margin: 0;
                background-color: #0a0b0e;
                color: #f5f7fa;
                font-family: sans-serif;
                display: flex;
                flex-direction: column;
                align-items: center;
                justify-content: center;
                min-height: 100vh;
                padding: 20px;
                box-sizing: border-box;
              }
              img {
                max-width: 95vw;
                max-height: 85vh;
                border-radius: 8px;
                box-shadow: 0 10px 40px rgba(0,0,0,0.8);
                border: 1px solid #1e2530;
              }
              .caption {
                margin-top: 14px;
                font-size: 13px;
                color: #94a3b8;
                font-family: monospace;
              }
            </style>
          </head>
          <body>
            <img src="${dataUrl}" alt="${filename}" />
            <div class="caption">${filename} · Right-click image to save or copy</div>
          </body>
        </html>
      `)
      win.document.close()
    } else {
      toast.error('Popup blocked. Allow popups to open image in tab.')
    }
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="screenshot-modal-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm animate-in fade-in duration-150"
      onClick={onClose}
    >
      <div
        ref={modalRef}
        className="bg-[#111318] border border-[#1e2530] rounded-xl shadow-2xl max-w-2xl w-full flex flex-col overflow-hidden max-h-[90vh]"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-[#1e2530] bg-[#16181f]">
          <div className="flex items-center gap-2">
            <div className="w-7 h-7 rounded-lg bg-[#10b981]/15 border border-[#10b981]/30 flex items-center justify-center text-[#10b981]">
              <Camera size={14} />
            </div>
            <div>
              <h3 id="screenshot-modal-title" className="text-sm font-semibold text-[#f5f7fa]">
                Chart Screenshot
              </h3>
              <p className="text-[11px] text-slate-400 font-mono">
                {symbol} · {timeframe.toUpperCase()}
              </p>
            </div>
          </div>

          <button
            type="button"
            onClick={onClose}
            aria-label="Close screenshot preview"
            className="p-1 rounded-md text-slate-400 hover:text-white hover:bg-white/10 transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {/* Chart Image Preview */}
        <div className="p-4 bg-[#0a0b0e] flex items-center justify-center overflow-auto max-h-[55vh]">
          <img
            src={dataUrl}
            alt={`${symbol} chart screenshot`}
            className="rounded border border-[#1e2530] shadow-lg max-h-[50vh] w-auto object-contain"
          />
        </div>

        {/* Footer Actions */}
        <div className="p-3 border-t border-[#1e2530] bg-[#16181f] flex flex-wrap items-center justify-between gap-2">
          <div className="text-[11px] text-slate-400 font-mono truncate max-w-[220px]">
            {filename}
          </div>

          <div className="flex items-center gap-2 flex-wrap">
            {/* Open in Tab */}
            <button
              type="button"
              onClick={handleOpenInNewTab}
              className="px-2.5 py-1.5 text-xs font-medium text-slate-300 hover:text-white hover:bg-white/10 rounded-md border border-[#2d3748] transition-colors flex items-center gap-1.5"
            >
              <ExternalLink size={13} />
              Open in Tab
            </button>

            {/* Copy Image */}
            <button
              type="button"
              onClick={handleCopy}
              className={`px-3 py-1.5 text-xs font-medium rounded-md border transition-colors flex items-center gap-1.5 ${
                copied
                  ? 'bg-emerald-500/20 text-emerald-400 border-emerald-500/40'
                  : 'bg-[#1e2530] text-slate-200 hover:text-white hover:bg-[#252e3b] border-[#2d3748]'
              }`}
            >
              {copied ? <Check size={13} /> : <Copy size={13} />}
              {copied ? 'Copied!' : 'Copy Image'}
            </button>

            {/* Save PNG */}
            <button
              type="button"
              onClick={handleSave}
              disabled={saving}
              className="px-3.5 py-1.5 text-xs font-semibold bg-[#10b981] hover:bg-[#059669] text-[#0a0b0e] rounded-md transition-colors flex items-center gap-1.5 shadow-sm disabled:opacity-50"
            >
              <Download size={13} />
              Save PNG
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
