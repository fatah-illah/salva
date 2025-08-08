package tests

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"multi-tenant-messaging/internal/domain"
	"multi-tenant-messaging/internal/handler"
	"multi-tenant-messaging/internal/repository"
	"multi-tenant-messaging/internal/service"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var db *sql.DB
var rabbitConn *amqp.Connection
var rabbitChannel *amqp.Channel

func TestMain(m *testing.M) {
	// Setup Docker pool
	pool, err := dockertest.NewPool("")
	if err != nil {
		fmt.Printf("Could not connect to docker: %s\n", err)
		os.Exit(1)
	}

	// Start PostgreSQL container
	pgResource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "13",
		Env: []string{
			"POSTGRES_USER=postgres",
			"POSTGRES_PASSWORD=postgres",
			"POSTGRES_DB=test",
		},
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		fmt.Printf("Could not start resource: %s\n", err)
		os.Exit(1)
	}

	pgURL := fmt.Sprintf("postgres://postgres:postgres@localhost:%s/test?sslmode=disable", pgResource.GetPort("5432/tcp"))

	// Wait for PostgreSQL to be ready
	if err := pool.Retry(func() error {
		var err error
		db, err = sql.Open("postgres", pgURL)
		if err != nil {
			return err
		}
		return db.Ping()
	}); err != nil {
		fmt.Printf("Could not connect to PostgreSQL: %s\n", err)
		os.Exit(1)
	}

	// Run migrations
	runMigrations(db)

	// Start RabbitMQ container
	rabbitResource, err := pool.Run("rabbitmq", "3-management", []string{})
	if err != nil {
		fmt.Printf("Could not start resource: %s\n", err)
		os.Exit(1)
	}

	rabbitURL := fmt.Sprintf("amqp://guest:guest@localhost:%s/", rabbitResource.GetPort("5672/tcp"))

	// Wait for RabbitMQ to be ready
	if err := pool.Retry(func() error {
		var err error
		rabbitConn, err = amqp.Dial(rabbitURL)
		if err != nil {
			return err
		}
		rabbitChannel, err = rabbitConn.Channel()
		return err
	}); err != nil {
		fmt.Printf("Could not connect to RabbitMQ: %s\n", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup
	rabbitChannel.Close()
	rabbitConn.Close()
	db.Close()
	pool.Purge(pgResource)
	pool.Purge(rabbitResource)

	os.Exit(code)
}

func runMigrations(db *sql.DB) {
	// Execute the schema
	_, err := db.Exec(`
		CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

		CREATE TABLE IF NOT EXISTS tenants (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS messages (
			id UUID NOT NULL,
			tenant_id UUID NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (id, tenant_id)
		) PARTITION BY LIST (tenant_id);

		CREATE TABLE IF NOT EXISTS tenant_configs (
			tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
			workers INT NOT NULL DEFAULT 3
		);
	`)
	if err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}
}

func setupRouter() *gin.Engine {
	// Setup dependencies
	dbRepo := &repository.Database{DB: db}
	rabbitRepo := &repository.RabbitMQ{
		Conn:    rabbitConn,
		Channel: rabbitChannel,
	}

	tenantManager := domain.NewTenantManager()
	tenantService := service.NewTenantService(dbRepo, rabbitRepo, tenantManager)
	tenantHandler := handler.NewTenantHandler(tenantService)
	messageHandler := handler.NewMessageHandler(dbRepo)

	router := gin.Default()
	router.POST("/tenants", tenantHandler.CreateTenant)
	router.DELETE("/tenants/:id", tenantHandler.DeleteTenant)
	router.PUT("/tenants/:id/config/concurrency", tenantHandler.UpdateConcurrency)
	router.GET("/messages", messageHandler.ListMessages)

	return router
}

func TestTenantLifecycle(t *testing.T) {
	router := setupRouter()

	// Create tenant
	tenant := domain.Tenant{Name: "Test Tenant"}
	tenantJSON, _ := json.Marshal(tenant)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tenants", bytes.NewBuffer(tenantJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var createdTenant domain.Tenant
	json.Unmarshal(w.Body.Bytes(), &createdTenant)
	assert.NotEmpty(t, createdTenant.ID)

	// Update concurrency
	configJSON := `{"workers": 5}`
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", fmt.Sprintf("/tenants/%s/config/concurrency", createdTenant.ID), bytes.NewBufferString(configJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Delete tenant
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("/tenants/%s", createdTenant.ID), nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestMessagePublishingConsumption(t *testing.T) {
	router := setupRouter()

	// Create tenant
	tenant := domain.Tenant{Name: "Message Test Tenant"}
	tenantJSON, _ := json.Marshal(tenant)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tenants", bytes.NewBuffer(tenantJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var createdTenant domain.Tenant
	json.Unmarshal(w.Body.Bytes(), &createdTenant)

	// Publish message to tenant queue
	queueName := fmt.Sprintf("tenant_%s_queue", createdTenant.ID)
	_, err := rabbitChannel.QueueDeclare(
		queueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // args
	)
	assert.NoError(t, err)

	messageBody := `{"text": "Test message"}`
	err = rabbitChannel.Publish(
		"",        // exchange
		queueName, // routing key
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        []byte(messageBody),
		},
	)
	assert.NoError(t, err)

	// Wait for message to be processed
	time.Sleep(1 * time.Second)

	// List messages
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/messages", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data       []domain.Message `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Greater(t, len(response.Data), 0)

	// Bandingkan payload sebagai map
	expectedPayload := map[string]interface{}{"text": "Test message"}
	actualPayload := response.Data[0].Payload

	// Konversi ke JSON string untuk perbandingan yang akurat
	expectedJSON, _ := json.Marshal(expectedPayload)
	actualJSON, _ := json.Marshal(actualPayload)
	assert.JSONEq(t, string(expectedJSON), string(actualJSON))

	// Cleanup: Delete tenant
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("/tenants/%s", createdTenant.ID), nil)
	router.ServeHTTP(w, req)
}

func TestConcurrencyConfigUpdate(t *testing.T) {
	router := setupRouter()

	// Create tenant
	tenant := domain.Tenant{Name: "Concurrency Test Tenant"}
	tenantJSON, _ := json.Marshal(tenant)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tenants", bytes.NewBuffer(tenantJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var createdTenant domain.Tenant
	json.Unmarshal(w.Body.Bytes(), &createdTenant)

	// Update concurrency to 5
	configJSON := `{"workers": 5}`
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", fmt.Sprintf("/tenants/%s/config/concurrency", createdTenant.ID), bytes.NewBufferString(configJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Publish multiple messages
	queueName := fmt.Sprintf("tenant_%s_queue", createdTenant.ID)
	for i := 0; i < 10; i++ {
		messageBody := fmt.Sprintf(`{"message": "Test message %d"}`, i)
		err := rabbitChannel.Publish(
			"",        // exchange
			queueName, // routing key
			false,     // mandatory
			false,     // immediate
			amqp.Publishing{
				ContentType: "application/json",
				Body:        []byte(messageBody),
			},
		)
		assert.NoError(t, err)
	}

	// Wait for messages to be processed
	time.Sleep(2 * time.Second)

	// List messages
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/messages", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data       []domain.Message `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, 10, len(response.Data))

	// Cleanup: Delete tenant
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("/tenants/%s", createdTenant.ID), nil)
	router.ServeHTTP(w, req)
}
