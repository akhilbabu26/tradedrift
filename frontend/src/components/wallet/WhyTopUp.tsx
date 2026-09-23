import { BarChart3, ShieldCheck, Clock, Gift } from 'lucide-react'

const BENEFITS = [
  {
    icon: BarChart3,
    iconColor: 'text-[#10b981]',
    iconBg: 'bg-[#10b981]/10 border-[#10b981]/20',
    title: 'Get more simulated funds',
    desc: 'Continue trading when you run low.',
  },
  {
    icon: ShieldCheck,
    iconColor: 'text-[#14b8a6]',
    iconBg: 'bg-[#14b8a6]/10 border-[#14b8a6]/20',
    title: 'Secure Payments',
    desc: 'Powered by Razorpay (UPI, Card, Net Banking).',
  },
  {
    icon: Clock,
    iconColor: 'text-[#3b82f6]',
    iconBg: 'bg-[#3b82f6]/10 border-[#3b82f6]/20',
    title: 'Daily Limit Protection',
    desc: 'You can top up up to 10,000 USDT per day.',
  },
  {
    icon: Gift,
    iconColor: 'text-[#06b6d4]',
    iconBg: 'bg-[#06b6d4]/10 border-[#06b6d4]/20',
    title: 'Start with a Welcome Bonus',
    desc: 'New users get 10,000 USDT free on signup.',
  },
]

export default function WhyTopUp() {
  return (
    <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex flex-col justify-between shadow-sm">
      <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
        Why Top Up?
      </h2>

      <div className="mt-3 flex flex-col gap-3">
        {BENEFITS.map((b) => {
          const Icon = b.icon
          return (
            <div key={b.title} className="flex items-start gap-3">
              <div className={`w-8 h-8 rounded-lg ${b.iconBg} border flex items-center justify-center flex-shrink-0 mt-0.5`}>
                <Icon size={15} className={b.iconColor} />
              </div>
              <div className="flex flex-col">
                <span className="text-xs font-bold text-[#f5f7fa]">
                  {b.title}
                </span>
                <span className="text-[11px] text-slate-300 mt-0.5">
                  {b.desc}
                </span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
