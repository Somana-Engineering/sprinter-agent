package main

import (
	"flag"
	"log"

	"somana-agent/internal/config"
	"somana-agent/internal/database"
	"somana-agent/internal/services"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("config", "config/config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Initialize database
	if err := database.InitDatabase(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Create host registration service
	hostRegService := services.NewHostRegistrationService(cfg)

	// Start host registration and heartbeat
	if err := hostRegService.Start(); err != nil {
		log.Printf("Warning: Failed to start host registration: %v", err)
	}

	// Comment out Gin server for now - focus on host registration debugging
	/*
	// Create Gin router
	r := gin.Default()

	// Create host service that implements the generated ServerInterface
	hostService := services.NewHostService()

	// Register handlers using the generated code
	generated.RegisterHandlers(r, hostService)

	// Start server
	log.Println("Starting Somana Agent server on :9000")
	if err := r.Run(":9000"); err != nil {
		log.Fatal("Failed to start server:", err)
	}
	*/

	// Keep the process running for debugging
	log.Println("Host registration service started. Press Ctrl+C to exit.")
	select {}
} 