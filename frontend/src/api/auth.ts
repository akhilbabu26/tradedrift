import client from './client'

export interface User {
  userId: string
  email: string
  username: string
}

export interface LoginRequest {
  identifier: string   // email or username — matches backend DTO
  password: string
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  accessTokenExpiresAt?: string
  refreshTokenExpiresAt?: string
  user: User
}

export interface RegisterRequest {
  email: string
  username: string
  password: string
}

export interface RegisterResponse {
  userId: string
  verificationRequired: boolean
}

export interface GoogleLoginSuccessResponse {
  status: 'SUCCESS'
  user: User
  accessToken: string
  refreshToken: string
  accessTokenExpiresAt?: string
  refreshTokenExpiresAt?: string
}

export interface GoogleLoginLinkRequiredResponse {
  status: 'LINK_REQUIRED'
  linkToken: string
  email: string
  message: string
}

export type GoogleLoginResponse = GoogleLoginSuccessResponse | GoogleLoginLinkRequiredResponse

export interface LinkGoogleRequest {
  email: string
  password: string
  linkToken: string
}

export interface ConnectGoogleResponse {
  success: boolean
  provider: string
  email: string
}

export const authApi = {
  login: (data: LoginRequest) =>
    client.post<LoginResponse>('/api/v1/auth/login', data),

  register: (data: RegisterRequest) =>
    client.post<RegisterResponse>('/api/v1/auth/register', data),

  googleLogin: (idToken: string) =>
    client.post<GoogleLoginResponse>('/api/v1/auth/google', { idToken }),

  linkGoogle: (data: LinkGoogleRequest) =>
    client.post<LoginResponse>('/api/v1/auth/google/link', data),

  connectGoogle: (idToken: string) =>
    client.post<ConnectGoogleResponse>('/api/v1/auth/google/connect', { idToken }),

  verifyEmail: (data: { email: string; code: string }) =>
    client.post<{ user: User; accessToken: string; refreshToken: string }>('/api/v1/auth/verify', data),

  resendVerification: (data: { email: string }) =>
    client.post<{ success: boolean }>('/api/v1/auth/resend', data),

  forgotPassword: (data: { email: string }) =>
    client.post<{ success: boolean }>('/api/v1/auth/forgot-password', data),

  resetPassword: (data: { email: string; code: string; newPassword: string }) =>
    client.post<{ success: boolean }>('/api/v1/auth/reset-password', data),

  changePassword: (data: { oldPassword: string; newPassword: string }) =>
    client.post<{ success: boolean }>('/api/v1/auth/change-password', data),

  logout: () => {
    const refreshToken = localStorage.getItem('refresh_token')
    return client.post<{ success: boolean }>('/api/v1/auth/logout', { refreshToken })
  },

  logoutAll: () =>
    client.post<{ success: boolean }>('/api/v1/auth/logout-all'),

  refresh: (refreshToken: string) =>
    client.post<{ accessToken: string; refreshToken: string }>('/api/v1/auth/refresh', { refreshToken }),
}

