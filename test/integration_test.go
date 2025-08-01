package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fatah-illah/salva/internal/app"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/rabbitmq/amqp091-go"
)

func generateJWT(secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "integration-test",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})
	return token.SignedString(secret)
}

func TestIntegration(t *testing.T) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}

	pgResource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "13",
		Env: []string{
			"POSTGRES_PASSWORD=pass",
			"POSTGRES_DB=app",
		},
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("Could not start postgres: %s", err)
	}
	defer pool.Purge(pgResource)

	pgURL := fmt.Sprintf("postgres://postgres:pass@localhost:%s/app?sslmode=disable", pgResource.GetPort("5432/tcp"))
	var dbpool *pgxpool.Pool
	if err := pool.Retry(func() error {
		var err error
		dbpool, err = pgxpool.New(context.Background(), pgURL)
		if err != nil {
			return err
		}
		return dbpool.Ping(context.Background())
	}); err != nil {
		t.Fatalf("Could not connect to postgres: %s", err)
	}
	defer dbpool.Close()

	rmqResource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "rabbitmq",
		Tag:        "3-management",
		Env: []string{
			"RABBITMQ_DEFAULT_USER=user",
			"RABBITMQ_DEFAULT_PASS=pass",
		},
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("Could not start rabbitmq: %s", err)
	}
	defer pool.Purge(rmqResource)

	rmqURL := fmt.Sprintf("amqp://user:pass@localhost:%s/", rmqResource.GetPort("5672/tcp"))
	var amqpConn *amqp091.Connection
	if err := pool.Retry(func() error {
		var err error
		amqpConn, err = amqp091.Dial(rmqURL)
		return err
	}); err != nil {
		t.Fatalf("Could not connect to rabbitmq: %s", err)
	}
	defer amqpConn.Close()

	secret := []byte("7rT670rv1GA44eNO4zfzEgpKAOQvFL+NCmKuRWugTDY=")
	jwtToken, err := generateJWT(secret)
	if err != nil {
		t.Fatalf("Failed to generate JWT: %v", err)
	}
	authHeader := "Bearer " + jwtToken

	tenantID := "11111111-1111-1111-1111-111111111111"

	// Set ENV agar main.go membaca config dari docker container
	os.Setenv("DATABASE_URL", pgURL)
	os.Setenv("RABBITMQ_URL", rmqURL)
	os.Setenv("JWT_SECRET", string(secret))

	// Jalankan migrasi tabel messages
	_, err = dbpool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS messages (
	  id UUID,
	  tenant_id UUID NOT NULL,
	  payload JSONB,
	  created_at TIMESTAMPTZ DEFAULT NOW(),
	  PRIMARY KEY (tenant_id, id)
	) PARTITION BY LIST (tenant_id);
	`)
	if err != nil {
		t.Fatalf("Failed to migrate messages table: %v", err)
	}
	// Buat partisi untuk tenantID
	partitionTable := fmt.Sprintf("messages_tenant_%s", strings.ReplaceAll(tenantID, "-", "_"))
	partitionSQL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF messages FOR VALUES IN ('%s');`, partitionTable, tenantID)
	_, err = dbpool.Exec(context.Background(), partitionSQL)
	if err != nil {
		t.Fatalf("Failed to create partition for tenant: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run()
	}()

	// Optionally, you can check for errors after the test or after the server is ready

	// Tunggu server ready
	ready := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get("http://localhost:8080/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ready {
		t.Fatalf("HTTP server not ready")
	}

	apiURL := "http://localhost:8080"
	req, _ := http.NewRequest("POST", apiURL+"/tenants?id="+tenantID, nil)
	req.Header.Set("Authorization", authHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		body, _ := ioutil.ReadAll(resp.Body)
		t.Fatalf("Failed to create tenant: %v, %s", err, string(body))
	}
	resp.Body.Close()

	ch, err := amqpConn.Channel()
	if err != nil {
		t.Fatalf("Failed to open channel: %v", err)
	}
	defer ch.Close()

	queueName := fmt.Sprintf("tenant_%s_queue", tenantID)
	body := []byte(`{"hello": "world"}`)
	err = ch.Publish("", queueName, false, false, amqp091.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	found := false
	for i := 0; i < 10; i++ {
		rows, err := dbpool.Query(context.Background(), "SELECT payload FROM messages WHERE tenant_id = $1", tenantID)
		if err == nil && rows.Next() {
			var payload []byte
			rows.Scan(&payload)
			if string(payload) == string(body) {
				found = true
				break
			}
		}
		rows.Close()
		time.Sleep(1 * time.Second)
	}
	if !found {
		t.Fatalf("Message not found in DB for tenant %s", tenantID)
	}
	t.Logf("Message successfully published and consumed for tenant %s", tenantID)

	// Baca error dari errCh agar test tidak hang
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("App exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		// timeout, biarkan test exit
	}
}
