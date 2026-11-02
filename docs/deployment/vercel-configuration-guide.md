# TradeDrift — Vercel Frontend Configuration Guide

**Frontend URL:** [https://tradedrift-frontend.vercel.app/](https://tradedrift-frontend.vercel.app/)  
**Framework:** React 19 + TypeScript + Vite  

---

## 1. Required Vercel Environment Variables

In your Vercel Dashboard, navigate to **Project Settings → Environment Variables** and configure the following variables:

| Variable Name | Environment | Required? | Example Value | Description |
| :--- | :---: | :---: | :--- | :--- |
| **`VITE_GOOGLE_CLIENT_ID`** | Production, Preview | **Yes** | `778142269213-7e4knagb3r5cfhq2kdrj6nr8qlueobr4.apps.googleusercontent.com` | Google OAuth 2.0 Web Client ID |
| **`VITE_RAZORPAY_KEY_ID`** | Production, Preview | **Yes** | `rzp_test_TmBexil6Ccw0JO` | Razorpay checkout public key |
| **`VITE_API_BASE_URL`** | Production, Preview | **Yes** | `https://api.yourdomain.com` | Public HTTPS URL of the AWS API Gateway |
| **`VITE_WS_URL`** | Production, Preview | **Yes** | `wss://api.yourdomain.com/ws` | Public WSS URL of the WebSocket Gateway |

> [!IMPORTANT]
> **No Trailing Slashes & No `/api/v1`:**
> In [`frontend/src/api/client.ts`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/client.ts#L18), routes append `/api/v1/...` automatically. Set `VITE_API_BASE_URL` to `https://api.yourdomain.com` without `/api/v1` or a trailing slash `/`.

---

## 2. Triggering a Production Redeployment

Because Vite injects `import.meta.env` values at build time (static compilation):
1. Navigate to **Deployments** in your Vercel project dashboard.
2. Click the three dots (`...`) next to the current deployment.
3. Select **Redeploy**.
4. Leave "Use existing Build Cache" **unchecked** so that Vite re-bundles the new environment variables into the static JavaScript chunks.

---

## 3. Google OAuth 2.0 Authorized Origins

For Google Sign-In to function without `redirect_uri_mismatch` or `origin_mismatch` errors:
1. Open the [Google Cloud Console Credentials](https://console.cloud.google.com/apis/credentials).
2. Click your OAuth 2.0 Client ID (`778142269213-7e4knagb3r5cfhq2kdrj6nr8qlueobr4.apps.googleusercontent.com`).
3. Under **Authorized JavaScript origins**, add:
   * `https://tradedrift-frontend.vercel.app`
   * `http://localhost:5173` (for local development)
4. Under **Authorized redirect URIs**, add:
   * `https://tradedrift-frontend.vercel.app`
   * `https://tradedrift-frontend.vercel.app/login`
5. Click **Save** (takes 2–5 minutes for Google's edge cache to propagate).

---

## 4. Single-Page Application (SPA) Routing Verification

TradeDrift uses `react-router-dom` for client-side routing. Direct visits or page refreshes on `/trade`, `/portfolio`, `/orders`, or `/wallet` require server rewrites so Vercel doesn't return HTTP 404.

This is governed by [`frontend/vercel.json`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/vercel.json):
```json
{
  "rewrites": [
    {
      "source": "/(.*)",
      "destination": "/index.html"
    }
  ]
}
```
This file is committed to Git and automatically applied by Vercel during deployment.

