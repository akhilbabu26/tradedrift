package ws

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Handler handles HTTP requests to upgrade to WebSocket sessions.
type Handler struct {
	hub        *Hub
	jwtSecret  []byte
	corsOrigin string
	upgrader   websocket.Upgrader
	logger     *zap.Logger
}

// NewHandler creates a new WebSocket Handler.
func NewHandler(hub *Hub, jwtSecret string, corsOrigin string, logger *zap.Logger) *Handler {
	h := &Handler{
		hub:        hub,
		jwtSecret:  []byte(jwtSecret),
		corsOrigin: corsOrigin,
		logger:     logger,
	}

	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     h.checkOrigin,
	}
	return h
}

func (h *Handler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients
	}

	// Match configured CORS origin
	if h.corsOrigin != "" && (origin == h.corsOrigin || h.corsOrigin == "*") {
		return true
	}

	// Default local dev origins
	if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
		return true
	}

	h.logger.Warn("Rejected WebSocket connection from unauthorized origin", zap.String("origin", origin))
	return false
}

// ServeWS handles GET /ws request upgrade.
//
// Authentication contract (Bug Fix #5):
//   - No token provided     → Anonymous connection, allowed for public streams.
//   - Valid token provided  → Authenticated connection with userID set.
//   - Token provided but INVALID → HTTP 401 before upgrade. Connection rejected.
//
// Previously a tampered or expired token was silently treated as anonymous,
// which is misleading from an API-contract perspective.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Extract token if provided in query param or header
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	var userID string
	if tokenStr != "" {
		extractedUserID, err := h.parseToken(tokenStr)
		if err != nil {
			h.logger.Debug("Rejected WebSocket: invalid JWT in handshake", zap.Error(err))
			http.Error(w, "401 Unauthorized: invalid or expired token", http.StatusUnauthorized)
			return
		}
		userID = extractedUserID
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade WebSocket connection", zap.Error(err))
		return
	}

	client := NewClient(h.hub, conn, userID, h.logger)
	h.hub.Register(client)

	go client.WritePump()
	go client.ReadPump()
}

func (h *Handler) parseToken(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return h.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid token claims")
	}

	// Extract standard user_id / sub
	if sub, ok := claims["user_id"].(string); ok && sub != "" {
		return sub, nil
	}
	if sub, ok := claims["sub"].(string); ok && sub != "" {
		return sub, nil
	}

	return "", fmt.Errorf("missing user_id claim in token")
}
