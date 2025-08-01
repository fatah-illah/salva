package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rabbitmq/amqp091-go"
	"gopkg.in/yaml.v3"

	"github.com/fatah-illah/salva/api"
	"github.com/fatah-illah/salva/internal/tenant"
)

type Config struct {
	RabbitMQ struct {
		URL string `yaml:"url"`
	} `yaml:"rabbitmq"`
	Database struct {
		URL string `yaml:"url"`
	} `yaml:"database"`
	Workers   int    `yaml:"workers"`
	JWTSecret string `yaml:"jwt_secret"`
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg Config
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	cfg.Database.URL = getEnv("DATABASE_URL", cfg.Database.URL)
	cfg.RabbitMQ.URL = getEnv("RABBITMQ_URL", cfg.RabbitMQ.URL)
	cfg.JWTSecret = getEnv("JWT_SECRET", cfg.JWTSecret)
	return &cfg, nil
}

func Run() error {
	cfg, err := LoadConfig("config/config.yaml")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	fmt.Printf("Loaded config: %+v\n", cfg)

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

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	go func() {
		fmt.Println("Prometheus metrics on :2112/metrics")
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":2112", nil)
	}()

	fmt.Println("Starting HTTP server on :8080")
	return http.ListenAndServe(":8080", nil)
}

func main() {
	if err := Run(); err != nil {
		log.Fatal(err)
	}
}
