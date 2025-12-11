// Package main is the entrypoint for the alert2snow-agent application.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/logging"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
	"github.com/cragr/alert2snow-agent/internal/webhook"
)

var (
	// Prometheus metrics
	alertsReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alert2snow_alerts_received_total",
			Help: "Total number of alerts received from Alertmanager",
		},
		[]string{"status"},
	)
	serviceNowRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alert2snow_servicenow_requests_total",
			Help: "Total number of requests to ServiceNow",
		},
		[]string{"operation", "status"},
	)
)

func init() {
	prometheus.MustRegister(alertsReceived)
	prometheus.MustRegister(serviceNowRequests)
}

func main() {
	// Initialize logger
	logger := logging.NewLogger()
	logger.Info("starting alert2snow-agent")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("configuration loaded",
		"http_port", cfg.HTTPPort,
		"servicenow_base_url", cfg.ServiceNowBaseURL,
		"cluster_label_key", cfg.ClusterLabelKey,
		"environment_label_key", cfg.EnvironmentLabelKey,
	)

	// Create ServiceNow client
	snowClient := servicenow.NewClient(cfg, logging.WithComponent(logger, "servicenow"))

	// Create webhook handler
	transformer := webhook.NewTransformer(cfg)
	webhookHandler := webhook.NewHandler(snowClient, transformer, logging.WithComponent(logger, "webhook"))

	// Setup HTTP routes
	mux := http.NewServeMux()

	// Alertmanager webhook endpoint
	mux.Handle("/alertmanager/webhook", webhookHandler)

	// Health and readiness probes
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Create HTTP server
	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("HTTP server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

// healthzHandler handles liveness probe requests.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// readyzHandler handles readiness probe requests.
func readyzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
