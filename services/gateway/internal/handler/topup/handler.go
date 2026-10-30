package topup

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

// Handler implements a reverse proxy to the internal wallet-topup service.
// It injects the cryptographically validated UserID into the X-User-ID header
// for downstream authenticated requests, and strips any client-provided X-User-ID
// to prevent identity spoofing.
type Handler struct {
	proxy *httputil.ReverseProxy
}

// NewHandler constructs a new topup reverse proxy handler for the target URL.
func NewHandler(targetURL *url.URL, log *zap.Logger) *Handler {
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director

	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host

		// Inject the authenticated UserID from JWT context if present
		if userID := middleware.GetUserID(req); userID != "" {
			req.Header.Set("X-User-ID", userID)
		} else {
			// Strip any untrusted/spoofed X-User-ID header from incoming caller
			req.Header.Del("X-User-ID")
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		// Strip any upstream CORS headers so the Gateway remains the single public CORS authority
		// and prevents duplicate header values (e.g. Access-Control-Allow-Origin).
		for k := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				resp.Header.Del(k)
			}
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if log != nil {
			log.Error("Top-up reverse proxy error", zap.Error(err), zap.String("path", r.URL.Path))
		}
		response.WriteError(w, http.StatusBadGateway, "TOPUP_SERVICE_UNAVAILABLE", "wallet top-up service is currently unavailable")
	}

	return &Handler{proxy: proxy}
}

// Proxy forwards the HTTP request to the wallet-topup service.
func (h *Handler) Proxy(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}
