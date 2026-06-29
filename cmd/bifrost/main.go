package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jordinkolman/valkyrie-commerce/internal/queue"
	"github.com/jordinkolman/valkyrie-commerce/internal/config"
	"github.com/jordinkolman/valkyrie-commerce/internal/handlers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Setting up connection to Redis message queue...")

	redisURLStr := os.Getenv("REDIS_URL")
	if redisURLStr == "" {
		slog.Error("REDIS_URL environment variable missing")
		os.Exit(1)
	}
	client, err := queue.NewRedisClient(redisURLStr)

	if err != nil {
		slog.Error("Could not connect to Redis", "error", err.Error())
		os.Exit(1)
	}
	srv := handlers.NewServer(client)
	defer func() { _ = client.Close() }()
	slog.Info("Connected to Redis!")

	configPath := os.Getenv("PROVIDER_CONFIG_PATH")
	if configPath == "" {
		exePath, err := os.Executable()
		if err != nil {
			slog.Error("Fatal: Could not resolve executable path", "error", err)
			os.Exit(1)
		}
		configPath = filepath.Join(filepath.Dir(exePath), "config", "providers.json")
	}

	providers, err := config.LoadProviders(configPath)
	if err != nil {
		slog.Error("Fatal: Could not load provider configurations", "error", err)
		os.Exit(1)
	}
	slog.Info("Loaded webhook providers from config", "count", len(providers))

	mux := http.NewServeMux()
	for _, provider := range providers {
		route := fmt.Sprintf("POST /webhook/%s", provider.Name)
		mux.HandleFunc(route, srv.BuildWebhookHandler(provider))
	}

	portStr := os.Getenv("PORT")
	if portStr == "" {
		slog.Error("PORT environment variable missing")
		os.Exit(1)
	}

	port := fmt.Sprintf(":%s", portStr)
	httpServer := &http.Server{
		Addr:              port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// channel for receiving errors from server crash
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Summoning Bifrost", "port", port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Channel to receive server terminate request
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("Server crashed", "error", err)
		os.Exit(1)
	case <-quit:
		slog.Info("Kill signal received. Shutting down gracefully")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown abruptly", "error", err)
		os.Exit(1)
	}

	slog.Info("Bifrost exited properly.")
}

