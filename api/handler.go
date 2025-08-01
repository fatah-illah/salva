package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/fatah-illah/salva/internal/tenant"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rabbitmq/amqp091-go"
)

type TenantManagerWithAMQP interface {
	AddTenantWithAMQP(id string, conn *amqp091.Connection) error
	RemoveTenantWithAMQP(id string)
	UpdateConcurrency(id string, workers int)
	GetMessages(ctx context.Context, cursor string, limit int) (msgs []tenant.Message, nextCursor string, err error)
}

type Handler struct {
	TenantManager TenantManagerWithAMQP
	AMQPConn      *amqp091.Connection
	JWTSecret     string
}

func NewHandler(tm TenantManagerWithAMQP, amqpConn *amqp091.Connection, jwtSecret string) *Handler {
	return &Handler{TenantManager: tm, AMQPConn: amqpConn, JWTSecret: jwtSecret}
}

type jwtContextKey struct{}

func (h *Handler) JWTAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing token"})
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			return []byte(h.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), jwtContextKey{}, token))
		next(w, r)
	}
}

// CreateTenant godoc
// @Summary Create tenant
// @Description Create a new tenant and spawn consumer
// @Tags tenants
// @Accept json
// @Produce json
// @Param id query string true "Tenant ID"
// @Success 201 {object} map[string]string
// @Failure 400,500 {object} map[string]string
// @Router /tenants [post]
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing id"})
		return
	}
	if err := h.TenantManager.AddTenantWithAMQP(id, h.AMQPConn); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// DeleteTenant godoc
// @Summary Delete tenant
// @Description Delete a tenant and stop consumer
// @Tags tenants
// @Param id query string true "Tenant ID"
// @Success 204 {string} string "No Content"
// @Failure 400,500 {object} map[string]string
// @Router /tenants [delete]
func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing id"})
		return
	}
	h.TenantManager.RemoveTenantWithAMQP(id)
	w.WriteHeader(http.StatusNoContent)
}

type ConcurrencyRequest struct {
	Workers int `json:"workers"`
}

// UpdateConcurrency godoc
// @Summary Update tenant concurrency
// @Description Update worker count for tenant
// @Tags tenants
// @Accept json
// @Produce json
// @Param id query string true "Tenant ID"
// @Param body body ConcurrencyRequest true "Concurrency config"
// @Success 200 {object} map[string]interface{}
// @Failure 400,500 {object} map[string]string
// @Router /tenants/config/concurrency [put]
func (h *Handler) UpdateConcurrency(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing id"})
		return
	}
	var req ConcurrencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid body"})
		return
	}
	h.TenantManager.UpdateConcurrency(id, req.Workers)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"id": id, "workers": req.Workers})
}

// GetMessages godoc
// @Summary Get messages
// @Description Get messages with cursor pagination
// @Tags messages
// @Produce json
// @Param cursor query string false "Cursor"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /messages [get]
func (h *Handler) GetMessages(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("cursor")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 || limit > 100 {
			limit = 20
		}
	}
	msgs, nextCursor, err := h.TenantManager.GetMessages(r.Context(), cursor, limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]interface{}{
		"data":        msgs,
		"next_cursor": nextCursor,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
