import { GoogleLogin, type CredentialResponse } from '@react-oauth/google'
import toast from 'react-hot-toast'

export interface GoogleAuthButtonProps {
  onSuccess: (idToken: string) => void
  onError?: (errMessage: string) => void
  text?: 'signin_with' | 'signup_with' | 'continue_with' | 'signin'
  disabled?: boolean
}

/**
 * Google authentication button wrapped with TradeDrift design styling.
 * Returns the Google ID token (JWT) to be validated by the TradeDrift Auth Service.
 */
export default function GoogleAuthButton({
  onSuccess,
  onError,
  text = 'continue_with',
  disabled = false,
}: GoogleAuthButtonProps) {
  const handleSuccess = (credentialResponse: CredentialResponse) => {
    if (!credentialResponse.credential) {
      const msg = 'Google authentication succeeded but no ID token was provided.'
      toast.error(msg)
      onError?.(msg)
      return
    }
    onSuccess(credentialResponse.credential)
  }

  const handleError = () => {
    const msg = 'Google sign-in was unsuccessful or closed.'
    toast.error(msg)
    onError?.(msg)
  }

  return (
    <div
      className={`relative w-full h-11 sm:h-12 rounded-xl overflow-hidden bg-white hover:bg-neutral-100 transition-colors border border-slate-200 flex items-center justify-center shadow-sm cursor-pointer ${
        disabled ? 'opacity-50 pointer-events-none' : ''
      }`}
    >
      {/* Visual reference-matching button */}
      <div className="flex items-center justify-center gap-3 select-none pointer-events-none">
        <svg className="w-5 h-5 flex-shrink-0" viewBox="0 0 24 24">
          <path
            fill="#4285F4"
            d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
          />
          <path
            fill="#34A853"
            d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
          />
          <path
            fill="#FBBC05"
            d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.06H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.94l2.85-2.22.81-.63z"
          />
          <path
            fill="#EA4335"
            d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.06l3.66 2.84c.87-2.6 3.3-4.52 6.16-4.52z"
          />
        </svg>
        <span className="text-[#1F2937] font-semibold text-sm sm:text-base">
          {text === 'signup_with'
            ? 'Sign up with Google'
            : text === 'signin' || text === 'signin_with'
            ? 'Sign in with Google'
            : 'Continue with Google'}
        </span>
      </div>

      {/* Google Identity Services overlay */}
      <div className="absolute inset-0 w-full h-full opacity-[0.01] flex items-center justify-center overflow-hidden [&_iframe]:!w-full [&_iframe]:!h-full [&_iframe]:!min-w-[400px] [&_iframe]:!min-h-[54px] [&_iframe]:!scale-150 cursor-pointer">
        <GoogleLogin
          onSuccess={handleSuccess}
          onError={handleError}
          theme="outline"
          shape="rectangular"
          size="large"
          text={text}
        />
      </div>
    </div>
  )
}
