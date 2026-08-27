import type { ReactNode } from 'react'
import AppSidebar from './AppSidebar'
import AppHeader from './AppHeader'

interface AppLayoutProps {
  children: ReactNode
}

export default function AppLayout({ children }: AppLayoutProps) {
  return (
    <div className="h-screen w-screen overflow-hidden flex bg-[#07090e] text-[#f8fafc] font-sans antialiased">
      {/* Shared Master Sidebar */}
      <AppSidebar />

      {/* Main Content Pane */}
      <div className="flex-1 flex flex-col min-w-0 h-screen overflow-hidden">
        {/* Shared Master Header */}
        <AppHeader />

        {/* Scrollable Page Body */}
        <main className="flex-1 overflow-y-auto bg-[#07090e] custom-scrollbar p-6">
          {children}
        </main>
      </div>
    </div>
  )
}
