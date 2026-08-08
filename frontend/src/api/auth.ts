import client from './client'

export interface LoginRequest {
  identifier: string   // email or username — matches backend DTO
  password: string
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  user: {
    id: string
    email: string
    username: string
  }
}

export interface RegisterRequest {
  email: string
  username: string
  password: string
}

export const authApi = {
  login: (data: LoginRequest) =>
    client.post<LoginResponse>('/api/v1/auth/login', data),

  register: (data: RegisterRequest) =>
    client.post('/api/v1/auth/register', data),

  verifyEmail: (data: { email: string; code: string }) =>
    client.post('/api/v1/auth/verify', data),

  resendVerification: (data: { email: string }) =>
    client.post('/api/v1/auth/resend', data),

  forgotPassword: (data: { email: string }) =>
    client.post('/api/v1/auth/forgot-password', data),

  resetPassword: (data: { email: string; code: string; newPassword: string }) =>
    client.post('/api/v1/auth/reset-password', data),

  changePassword: (data: { oldPassword: string; newPassword: string }) =>
    client.post('/api/v1/auth/change-password', data),

  logout: () => {
    const refreshToken = localStorage.getItem('refresh_token')
    return client.post('/api/v1/auth/logout', { refreshToken })
  },

  logoutAll: () =>
    client.post('/api/v1/auth/logout-all'),

  refresh: (refreshToken: string) =>
    client.post<LoginResponse>('/api/v1/auth/refresh', { refreshToken }),
}
