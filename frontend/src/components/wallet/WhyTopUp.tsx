import { BarChart3, ShieldCheck, Clock, Gift } from 'lucide-react'
import { RaisedSurface } from '../common/Surface'

const BENEFITS = [
  {
    icon: BarChart3,
    iconColor: 'text-[#10B981]',
    iconBg: 'bg-[#10B981]/10 border-[#10B981]/20',
    title: 'Get more simulated funds',
    desc: 'Continue trading when you run low.',
  },
  {
    icon: ShieldCheck,
    iconColor: 'text-[#14B8A6]',
    iconBg: 'bg-[#14B8A6]/10 border-[#14B8A6]/20',
    title: 'Secure Payments',
    desc: 'Powered by Razorpay (UPI, Card, Net Banking).',
  },
  {
    icon: Clock,
    iconColor: 'text-[#3B82F6]',
    iconBg: 'bg-[#3B82F6]/10 border-[#3B82F6]/20',
    title: 'Daily Limit Protection',
    desc: 'You can top up up to 10,000 USDT per day.',
  },
  {
    icon: Gift,
    iconColor: 'text-[#06B6D4]',
    iconBg: 'bg-[#06B6D4]/10 border-[#06B6D4]/20',
    title: 'Start with a Welcome Bonus',
    desc: 'New users get 10,000 USDT free on signup.',
  },
]

export default function WhyTopUp() {
  return (
    <RaisedSurface variant="raised-subtle" className="flex flex-col justify-between h-full">
      <div>
        <h2 className="text-base font-bold text-[#F2F3F5]">
          Why Top Up?
        </h2>
        <p className="text-[11px] text-[#777E87] mt-0.5">
          Virtual trading benefits & account safety
        </p>

        <div className="mt-4 flex flex-col gap-3.5">
          {BENEFITS.map((b) => {
            const Icon = b.icon
            return (
              <div key={b.title} className="flex items-start gap-3">
                <div className={`w-8 h-8 rounded-lg ${b.iconBg} border flex items-center justify-center flex-shrink-0 mt-0.5`}>
                  <Icon size={15} className={b.iconColor} />
                </div>
                <div className="flex flex-col min-w-0">
                  <span className="text-xs font-bold text-[#F2F3F5]">
                    {b.title}
                  </span>
                  <span className="text-[11px] text-[#A7ADB5] mt-0.5 leading-snug">
                    {b.desc}
                  </span>
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </RaisedSurface>
  )
}
