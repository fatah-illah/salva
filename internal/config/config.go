package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	RabbitMQ RabbitMQConfig `mapstructure:"rabbitmq"`
	Database DatabaseConfig `mapstructure:"database"`
	Workers  int            `mapstructure:"workers"`
	Server   ServerConfig   `mapstructure:"server"`
}

type RabbitMQConfig struct {
	URL string `mapstructure:"url"`
}

type DatabaseConfig struct {
	URL string `mapstructure:"url"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %v", err)
	}

	if rabbitMQURL := os.Getenv("RABBITMQ_URL"); rabbitMQURL != "" {
		config.RabbitMQ.URL = rabbitMQURL
	}
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		config.Database.URL = dbURL
	}

	return &config, nil
}
