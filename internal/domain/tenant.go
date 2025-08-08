package domain

import (
	"context"
	"sync"
)

type Tenant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type TenantConfig struct {
	TenantID string `json:"tenant_id"`
	Workers  int    `json:"workers"`
}

type TenantManager struct {
	mu            sync.RWMutex
	activeTenants map[string]*TenantContext
}

type TenantContext struct {
	CancelFunc context.CancelFunc
	Config     TenantConfig
}

func NewTenantManager() *TenantManager {
	return &TenantManager{
		activeTenants: make(map[string]*TenantContext),
	}
}

func (tm *TenantManager) AddTenant(tenantID string, ctx *TenantContext) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.activeTenants[tenantID] = ctx
}

func (tm *TenantManager) RemoveTenant(tenantID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if ctx, exists := tm.activeTenants[tenantID]; exists {
		ctx.CancelFunc()
		delete(tm.activeTenants, tenantID)
	}
}

func (tm *TenantManager) UpdateConfig(tenantID string, workers int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if ctx, exists := tm.activeTenants[tenantID]; exists {
		ctx.Config.Workers = workers
	}
}

func (tm *TenantManager) GetConfig(tenantID string) (TenantConfig, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	ctx, exists := tm.activeTenants[tenantID]
	if !exists {
		return TenantConfig{}, false
	}
	return ctx.Config, true
}
