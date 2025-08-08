package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "multi-tenant-messaging/cmd/server/docs" // Import generated docs
	"multi-tenant-messaging/internal/config"
	"multi-tenant-messaging/internal/domain"
	"multi-tenant-messaging/internal/handler"
	"multi-tenant-messaging/internal/repository"
	"multi-tenant-messaging/internal/service"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// @title Multi-Tenant Messaging System API
// @version 1.0
// @description This is a multi-tenant messaging system.
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /
func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := repository.NewDatabase(cfg.Database.URL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	rabbit, err := repository.NewRabbitMQ(cfg.RabbitMQ.URL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rabbit.Close()

	tenantManager := domain.NewTenantManager()
	tenantService := service.NewTenantService(db, rabbit, tenantManager)
	tenantHandler := handler.NewTenantHandler(tenantService)
	messageHandler := handler.NewMessageHandler(db)

	router := gin.Default()

	// Swagger endpoint
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// API endpoints
	router.POST("/tenants", tenantHandler.CreateTenant)
	router.DELETE("/tenants/:id", tenantHandler.DeleteTenant)
	router.PUT("/tenants/:id/config/concurrency", tenantHandler.UpdateConcurrency)
	router.GET("/messages", messageHandler.ListMessages)

	server := &http.Server{
		Addr:    cfg.Server.Port,
		Handler: router,
	}

	go func() {
		log.Printf("Server running on %s", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
}
