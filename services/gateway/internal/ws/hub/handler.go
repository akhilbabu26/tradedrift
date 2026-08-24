package hub

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

// checkOrigin validates the WebSocket Upgrade request's Origin header.
func (h *Handler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Non-browser clients
	}

	if h.corsOrigin == "*" {
		return true
	}

	allowed := h.corsOrigin
	if allowed == "" {
		allowed = "http://localhost:5173"
	}
	if origin == allowed {
		return true
	}

	h.logger.Warn("Rejected WebSocket connection: origin not in allowlist",
		zap.String("origin", origin),
		zap.String("allowed", allowed),
	)
	return false
}

// ServeWS handles WebSocket upgrade and session lifecycle.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Authentication Extraction: query param "?token=..." or "Authorization: Bearer ..."
	var rawToken string
	if t := r.URL.Query().Get("token"); t != "" {
		rawToken = t
	} else if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		rawToken = strings.TrimPrefix(authHeader, "Bearer ")
	}

	var userID string
	if rawToken != "" {
		claims, err := h.validateJWT(rawToken)
		if err != nil {
			h.logger.Warn("WebSocket upgrade rejected: invalid JWT token", zap.Error(err))
			http.Error(w, "Unauthorized: invalid authentication token", http.StatusUnauthorized)
			return
		}
		userID = claims.UserID
	}

	// Upgrade HTTP Connection to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade HTTP to WebSocket", zap.Error(err))
		return
	}

	// Create and register Client session
	client := NewClient(h.hub, conn, userID, h.logger)
	h.hub.Register(client)

	// Launch async pumping routines
	go client.WritePump()
	go client.ReadPump()

	h.logger.Info("WebSocket connection established",
		zap.String("user_id", userID),
		zap.String("remote_addr", r.RemoteAddr),
	)
}

type authClaims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

func (h *Handler) validateJWT(tokenString string) (*authClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &authClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return h.jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*authClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}
