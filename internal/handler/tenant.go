package handler

import (
	"net/http"
	"time"

	"multi-tenant-messaging/internal/domain"
	"multi-tenant-messaging/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TenantHandler handles tenant related requests
type TenantHandler struct {
	tenantService *service.TenantService
}

// NewTenantHandler creates a new TenantHandler
func NewTenantHandler(tenantService *service.TenantService) *TenantHandler {
	return &TenantHandler{tenantService: tenantService}
}

// CreateTenant godoc
// @Summary Create a new tenant
// @Description Create a new tenant with a unique ID and start a consumer for the tenant
// @Tags tenants
// @Accept  json
// @Produce  json
// @Param request body object{name=string} true "Tenant creation request"
// @Success 201 {object} domain.Tenant
// @Failure 400 {object} object "Invalid request body"
// @Failure 500 {object} object "Internal server error"
// @Router /tenants [post]
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	var request struct {
		Name string `json:"name" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenant := domain.Tenant{
		ID:        uuid.New().String(),
		Name:      request.Name,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	if err := h.tenantService.CreateTenant(&tenant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, tenant)
}

// DeleteTenant godoc
// @Summary Delete a tenant
// @Description Delete a tenant by ID and stop its consumer
// @Tags tenants
// @Accept  json
// @Produce  json
// @Param id path string true "Tenant ID"
// @Success 204
// @Failure 500 {object} object "Internal server error"
// @Router /tenants/{id} [delete]
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	tenantID := c.Param("id")
	if err := h.tenantService.DeleteTenant(tenantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// UpdateConcurrency godoc
// @Summary Update the concurrency for a tenant
// @Description Update the number of workers for a tenant's consumer
// @Tags tenants
// @Accept  json
// @Produce  json
// @Param id path string true "Tenant ID"
// @Param config body object{workers=int} true "Concurrency configuration"
// @Success 200
// @Failure 400 {object} object "Invalid request body"
// @Failure 500 {object} object "Internal server error"
// @Router /tenants/{id}/config/concurrency [put]
func (h *TenantHandler) UpdateConcurrency(c *gin.Context) {
	tenantID := c.Param("id")

	var config struct {
		Workers int `json:"workers"`
	}
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.tenantService.UpdateConcurrency(tenantID, config.Workers); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusOK)
}
