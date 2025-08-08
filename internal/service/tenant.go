package service

import (
	"context"
	"fmt"
	"log"
	"multi-tenant-messaging/internal/domain"
	"multi-tenant-messaging/internal/repository"
	"multi-tenant-messaging/internal/worker"
	"strings"
)

type TenantService struct {
	db            *repository.Database
	rabbit        *repository.RabbitMQ
	tenantManager *domain.TenantManager
}

func NewTenantService(db *repository.Database, rabbit *repository.RabbitMQ, tm *domain.TenantManager) *TenantService {
	return &TenantService{
		db:            db,
		rabbit:        rabbit,
		tenantManager: tm,
	}
}

func (s *TenantService) CreateTenant(tenant *domain.Tenant) error {
	// Create database partition
	if err := s.createPartition(tenant.ID); err != nil {
		return fmt.Errorf("failed to create partition: %w", err)
	}

	// Create RabbitMQ queue
	queueName := fmt.Sprintf("tenant_%s_queue", tenant.ID)
	_, err := s.rabbit.Channel.QueueDeclare(
		queueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	// Create worker pool
	ctx, cancel := context.WithCancel(context.Background())
	pool := worker.NewWorkerPool(3) // Default workers

	// Start consumer
	go s.consumeMessages(ctx, pool, queueName, tenant.ID)

	// Store in tenant manager
	s.tenantManager.AddTenant(tenant.ID, &domain.TenantContext{
		CancelFunc: cancel,
		Config: domain.TenantConfig{
			TenantID: tenant.ID,
			Workers:  3,
		},
	})

	// Save tenant to database
	_, err = s.db.DB.Exec(
		"INSERT INTO tenants (id, name) VALUES ($1, $2)",
		tenant.ID, tenant.Name,
	)
	return err
}

func (s *TenantService) DeleteTenant(tenantID string) error {
	s.tenantManager.RemoveTenant(tenantID)

	// Delete queue
	queueName := fmt.Sprintf("tenant_%s_queue", tenantID)
	_, err := s.rabbit.Channel.QueueDelete(
		queueName,
		false, // ifUnused
		false, // ifEmpty
		false, // noWait
	)
	if err != nil {
		log.Printf("Failed to delete queue: %v", err)
	}

	// Delete from database
	_, err = s.db.DB.Exec("DELETE FROM tenants WHERE id = $1", tenantID)
	return err
}

func (s *TenantService) UpdateConcurrency(tenantID string, workers int) error {
	s.tenantManager.UpdateConfig(tenantID, workers)
	// Actual worker pool update would be handled in the consumer goroutine
	return nil
}

func (s *TenantService) createPartition(tenantID string) error {
	// Normalize tenantID by replacing hyphens with underscores
	normalizedID := strings.ReplaceAll(tenantID, "-", "_")
	partitionName := fmt.Sprintf("messages_tenant_%s", normalizedID)

	// Gunakan quoted identifier untuk nama tabel
	_, err := s.db.DB.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS "%s" PARTITION OF messages
		FOR VALUES IN ('%s')
	`, partitionName, tenantID))

	return err
}

func (s *TenantService) consumeMessages(ctx context.Context, pool *worker.WorkerPool, queueName, tenantID string) {
	msgs, err := s.rabbit.Channel.Consume(
		queueName,
		"",    // consumer
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		log.Printf("Failed to consume messages: %v", err)
		return
	}

	go pool.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-msgs:
			if !ok {
				return
			}
			pool.Submit(func() {
				if err := s.processMessage(tenantID, d.Body); err != nil {
					log.Printf("Failed to process message: %v", err)
					d.Nack(false, true) // Requeue
				} else {
					d.Ack(false)
				}
			})
		}
	}
}

func (s *TenantService) processMessage(tenantID string, body []byte) error {
	_, err := s.db.DB.Exec(`
		INSERT INTO messages (id, tenant_id, payload) 
		VALUES (gen_random_uuid(), $1, $2)
	`, tenantID, body)
	return err
}
