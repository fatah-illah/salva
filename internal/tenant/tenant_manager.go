package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rabbitmq/amqp091-go"
)

var (
	messagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "messages_total",
			Help: "Total messages processed per tenant.",
		},
		[]string{"tenant_id"},
	)
	workerGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_workers",
			Help: "Number of active workers per tenant.",
		},
		[]string{"tenant_id"},
	)
	queueDepthGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_queue_depth",
			Help: "Current task queue depth per tenant.",
		},
		[]string{"tenant_id"},
	)
)

func init() {
	prometheus.MustRegister(messagesTotal, workerGauge, queueDepthGauge)
}

type TenantManager struct {
	mu        sync.Mutex
	consumers map[string]*TenantConsumer
	DB        *pgxpool.Pool
}

type TenantConsumer struct {
	ID              string
	StopCh          chan struct{}
	Workers         int
	TaskCh          chan interface{} // channel untuk tugas/pekerjaan
	wg              sync.WaitGroup
	workerStopChans []chan struct{} // untuk kontrol worker pool

	amqpChannel    *amqp091.Channel
	amqpDeliveryCh <-chan amqp091.Delivery
	consumerTag    string

	manager *TenantManager
}

type Message struct {
	ID        uuid.UUID
	TenantID  string
	Payload   json.RawMessage
	CreatedAt time.Time
}

func NewTenantManager() *TenantManager {
	return &TenantManager{
		consumers: make(map[string]*TenantConsumer),
	}
}

func (tm *TenantManager) insertMessage(ctx context.Context, tenantID string, payload []byte) error {
	_, err := tm.DB.Exec(ctx, `INSERT INTO messages (id, tenant_id, payload) VALUES ($1, $2, $3)`, uuid.New(), tenantID, payload)
	return err
}

func (tc *TenantConsumer) sendToDLQ(msg amqp091.Delivery) {
	if tc.amqpChannel == nil {
		return
	}
	dlqName := fmt.Sprintf("tenant_%s_dlq", tc.ID)
	err := tc.amqpChannel.Publish(
		"", dlqName, false, false,
		amqp091.Publishing{
			ContentType:  "application/json",
			Body:         msg.Body,
			DeliveryMode: amqp091.Persistent,
		},
	)
	if err == nil {
		msg.Ack(false)
	} else {
		msg.Nack(false, true)
	}
}

func (tc *TenantConsumer) startWorkers(n int) {
	workerGauge.WithLabelValues(tc.ID).Set(float64(n))
	tc.workerStopChans = make([]chan struct{}, n)
	for i := 0; i < n; i++ {
		stopCh := make(chan struct{})
		tc.workerStopChans[i] = stopCh
		tc.wg.Add(1)
		go func(idx int, stopCh chan struct{}) {
			defer tc.wg.Done()
			for {
				select {
				case <-stopCh:
					return
				case <-tc.StopCh:
					return
				case task, ok := <-tc.TaskCh:
					if !ok {
						return
					}
					// Proses pesan dari RabbitMQ
					if msg, ok := task.(amqp091.Delivery); ok {
						maxRetry := 3
						for attempt := 1; attempt <= maxRetry; attempt++ {
							var err error
							if tc.manager != nil && tc.manager.DB != nil {
								err = tc.manager.insertMessage(context.Background(), tc.ID, msg.Body)
							}
							if err == nil {
								messagesTotal.WithLabelValues(tc.ID).Inc()
								msg.Ack(false)
								break
							} else if attempt == maxRetry {
								tc.sendToDLQ(msg)
							}
							time.Sleep(time.Duration(attempt) * time.Second)
						}
					}
				}
			}
		}(i, stopCh)
	}
}

func (tc *TenantConsumer) stopWorkers() {
	workerGauge.WithLabelValues(tc.ID).Set(0)
	for _, stopCh := range tc.workerStopChans {
		close(stopCh)
	}
	tc.wg.Wait()
}

func (tc *TenantConsumer) updateWorkers(newN int) {
	workerGauge.WithLabelValues(tc.ID).Set(float64(newN))
	if newN == len(tc.workerStopChans) {
		return
	}
	tc.stopWorkers()
	tc.startWorkers(newN)
}

func (tm *TenantManager) AddTenant(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tc := &TenantConsumer{
		ID:      id,
		StopCh:  make(chan struct{}),
		Workers: 1, // default 1 worker
		TaskCh:  make(chan interface{}, 100),
	}
	tc.startWorkers(tc.Workers)
	tm.consumers[id] = tc
	// TODO: spawn consumer goroutine, dsb
}

func (tm *TenantManager) RemoveTenant(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if c, ok := tm.consumers[id]; ok {
		close(c.StopCh)
		c.stopWorkers()
		delete(tm.consumers, id)
		// TODO: cleanup RabbitMQ, dsb
	}
}

func (tm *TenantManager) UpdateConcurrency(id string, workers int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if c, ok := tm.consumers[id]; ok {
		c.Workers = workers
		c.updateWorkers(workers)
	}
}

func (tm *TenantManager) AddTenantWithAMQP(id string, conn *amqp091.Connection) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	queueName := fmt.Sprintf("tenant_%s_queue", id)
	dlqName := fmt.Sprintf("tenant_%s_dlq", id)
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	// Declare DLQ
	_, err = ch.QueueDeclare(dlqName, true, false, false, false, nil)
	if err != nil {
		ch.Close()
		return err
	}
	// Declare main queue with DLQ binding
	args := amqp091.Table{"x-dead-letter-exchange": "", "x-dead-letter-routing-key": dlqName}
	_, err = ch.QueueDeclare(queueName, true, false, false, false, args)
	if err != nil {
		ch.Close()
		return err
	}
	consumerTag := fmt.Sprintf("consumer_%s", id)
	deliveryCh, err := ch.Consume(queueName, consumerTag, false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return err
	}
	tc := &TenantConsumer{
		ID:             id,
		StopCh:         make(chan struct{}),
		Workers:        1,
		TaskCh:         make(chan interface{}, 100),
		amqpChannel:    ch,
		amqpDeliveryCh: deliveryCh,
		consumerTag:    consumerTag,
	}
	tc.manager = tm
	tc.startWorkers(tc.Workers)
	tm.consumers[id] = tc
	go tc.consumeLoop()
	return nil
}

func (tc *TenantConsumer) consumeLoop() {
	for {
		select {
		case <-tc.StopCh:
			return
		case msg, ok := <-tc.amqpDeliveryCh:
			if !ok {
				return
			}
			select {
			case tc.TaskCh <- msg:
				// pesan dikirim ke worker pool
			case <-tc.StopCh:
				return
			}
		}
	}
}

func (tm *TenantManager) RemoveTenantWithAMQP(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if c, ok := tm.consumers[id]; ok {
		close(c.StopCh)
		c.stopWorkers()
		if c.amqpChannel != nil {
			c.amqpChannel.Cancel(c.consumerTag, false)
			c.amqpChannel.QueueDelete(fmt.Sprintf("tenant_%s_queue", id), false, false, false)
			c.amqpChannel.Close()
		}
		delete(tm.consumers, id)
	}
}

func (tm *TenantManager) GetMessages(ctx context.Context, cursor string, limit int) ([]Message, string, error) {
	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = tm.DB.Query(ctx, `SELECT id, tenant_id, payload, created_at FROM messages ORDER BY created_at, id LIMIT $1`, limit+1)
	} else {
		rows, err = tm.DB.Query(ctx, `SELECT id, tenant_id, payload, created_at FROM messages WHERE created_at > (SELECT created_at FROM messages WHERE id = $1) OR (created_at = (SELECT created_at FROM messages WHERE id = $1) AND id > $1) ORDER BY created_at, id LIMIT $2`, cursor, limit+1)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	msgs := make([]Message, 0, limit)
	var nextCursor string
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Payload, &m.CreatedAt); err != nil {
			return nil, "", err
		}
		if len(msgs) < limit {
			msgs = append(msgs, m)
			nextCursor = m.ID.String()
		}
	}
	if len(msgs) < limit {
		nextCursor = ""
	}
	return msgs, nextCursor, nil
}
