export interface DeviceSession {
  id: string
  device: string
  browser: string
  os: string
  isCurrent: boolean
  location: string
  ip: string
  lastActive: string
  status: 'active' | 'inactive'
}

export interface PasswordRequirements {
  hasMinLength: boolean
  hasLetter: boolean
  hasNumber: boolean
}
