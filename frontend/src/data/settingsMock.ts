/**
 * Settings Demo / Mock Data
 *
 * NOTE: These are deterministic DEMO session records for UI preview.
 * There is currently no backend session tracking endpoint; these records
 * do not represent real hardware or live network geolocations.
 */

import type { DeviceSession } from '../types/settings'

export const DEMO_ACTIVE_SESSIONS: DeviceSession[] = [
  {
    id: 'sess_current_win',
    device: 'desktop',
    browser: 'Chrome',
    os: 'Windows',
    isCurrent: true,
    location: 'Wayanad, Kerala, India',
    ip: '192.168.1.24',
    lastActive: 'Active now',
    status: 'active',
  },
  {
    id: 'sess_safari_ios',
    device: 'mobile',
    browser: 'Safari',
    os: 'iPhone',
    isCurrent: false,
    location: 'Bengaluru, Karnataka, India',
    ip: '103.76.12.45',
    lastActive: '2 days ago',
    status: 'inactive',
  },
]
