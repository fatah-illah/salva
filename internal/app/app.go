package app

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rabbitmq/amqp091-go"

	"github.com/fatah-illah/salva/api"
	"github.com/fatah-illah/salva/internal/tenant"
)

type Config struct {
	RabbitMQ struct {
		URL string
	}
	Database struct {
		URL string
	}
	Workers   int
	JWTSecret string
}

func Run() error {
	cfg := &Config{
		RabbitMQ:  struct{ URL string }{URL: os.Getenv("RABBITMQ_URL")},
		Database:  struct{ URL string }{URL: os.Getenv("DATABASE_URL")},
		JWTSecret: os.Getenv("JWT_SECRET"),
		// Workers:   3, // default
	}

	dbpool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}
	defer dbpool.Close()
	fmt.Println("Connected to PostgreSQL")

	amqpConn, err := amqp091.Dial(cfg.RabbitMQ.URL)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}
	defer amqpConn.Close()
	fmt.Println("Connected to RabbitMQ")

	tm := tenant.NewTenantManager()
	tm.DB = dbpool
	h := api.NewHandler(tm, amqpConn, cfg.JWTSecret)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/tenants", h.JWTAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			h.CreateTenant(w, r)
		case http.MethodDelete:
			h.DeleteTenant(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	http.HandleFunc("/tenants/config/concurrency", h.JWTAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			h.UpdateConcurrency(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))

	http.HandleFunc("/messages", h.JWTAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			h.GetMessages(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":2112", nil)
	}()

	fmt.Println("Starting HTTP server on :8080")
	return http.ListenAndServe(":8080", nil)
}
