package repository

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

type Database struct {
	DB *sql.DB
}

func NewDatabase(url string) (*Database, error) {
	log.Printf("Connecting to database with URL: %s", url)

	var db *sql.DB
	var err error

	// Coba konek beberapa kali
	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", url)
		if err != nil {
			log.Printf("Connection attempt %d failed: %v", i+1, err)
			time.Sleep(2 * time.Second)
			continue
		}

		err = db.Ping()
		if err == nil {
			log.Println("Successfully connected to database")
			return &Database{DB: db}, nil
		}

		log.Printf("Ping attempt %d failed: %v", i+1, err)
		db.Close()
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("failed to connect to database after 5 attempts: %v", err)
}

func (d *Database) Close() {
	d.DB.Close()
}
