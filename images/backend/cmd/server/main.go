package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"velodb-demo/backend/internal/config"
	"velodb-demo/backend/internal/datagen/service"
	"velodb-demo/backend/internal/db"
	"velodb-demo/backend/internal/handlers"
	"velodb-demo/backend/internal/sync"
)

func main() {
	log.Println("[INFO] Loading configuration...")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[ERROR] Failed to load configuration: %v", err)
	}

	log.Println("[INFO] Connecting to VeloDB...")
	velodbClient, err := db.NewVeloDBClient(&cfg.VeloDB)
	if err != nil {
		log.Printf("[WARN] Failed to connect to VeloDB: %v - continuing without VeloDB", err)
		velodbClient = nil
	} else {
		defer velodbClient.Close()
		log.Println("[INFO] VeloDB connection established")
	}

	log.Println("[INFO] Connecting to Postgres...")
	postgresClient, err := db.NewPostgresClient(&cfg.Postgres)
	if err != nil {
		log.Fatalf("[ERROR] Failed to connect to Postgres: %v", err)
	}
	defer postgresClient.Close()
	log.Println("[INFO] Postgres connection established")

	// Initialize generator manager
	var generatorManager *service.Manager
	var generatorHandler *handlers.GeneratorHandler
	if cfg.Generator.Enabled {
		log.Println("[INFO] Initializing data generators...")
		generatorConfig := &service.Config{
			// Orders generator DISABLED - replaced by EcommerceService
			// This generated fact_orders which is no longer used
			// Orders: &service.OrderConfig{
			// 	Enabled:      true,
			// 	PartnerID:    cfg.Generator.PartnerID,
			// 	BaselineRate: cfg.Generator.OrdersBaselineRate,
			// 	DB:           postgresClient.GetDB(),
			// },
			Clickstream: &service.ClickstreamConfig{
				Enabled:      true,
				PartnerID:    cfg.Generator.PartnerID,
				BaselineRate: cfg.Generator.ClickstreamBaselineRate,
				KafkaBrokers: cfg.Generator.KafkaBrokers,
				KafkaTopic:   cfg.Generator.KafkaTopic,
				DB:           postgresClient.GetDB(), // For user ID validation
			},
			Ecommerce: &service.EcommerceConfig{
				Enabled:      true,
				BaselineRate: 10, // 10 orders per minute for Kibana demo
				DB:           postgresClient.GetDB(),
				// Kafka producer REMOVED - ecommerce orders use CDC sync (Postgres → VeloDB)
				// KafkaBrokers: cfg.Generator.KafkaBrokers,
				// KafkaTopic:   "ecommerce-orders",
			},
		}

		generatorManager, err = service.NewManager(generatorConfig)
		if err != nil {
			log.Fatalf("[ERROR] Failed to create generator manager: %v", err)
		}
		log.Println("[INFO] Data generators initialized")

		// Auto-start generators for continuous data generation
		log.Println("[INFO] Auto-starting data generators...")
		if err := generatorManager.Start(context.Background()); err != nil {
			log.Printf("[WARN] Failed to auto-start generators: %v", err)
		} else {
			log.Printf("[INFO] Data generators running - Orders: %d/min, Clickstream: %d/sec",
				cfg.Generator.OrdersBaselineRate, cfg.Generator.ClickstreamBaselineRate)
		}
	}

	// Legacy sync service REMOVED - fact_orders table no longer used
	// Now using EcommerceSyncService for kibana_sample_data_ecommerce
	log.Println("[INFO] Using EcommerceSyncService for kibana_sample_data_ecommerce")

	// Initialize clickstream sync service (Kafka -> VeloDB)
	var clickstreamSync *sync.ClickstreamSyncService
	if cfg.Generator.Enabled && len(cfg.Generator.KafkaBrokers) > 0 && velodbClient != nil {
		log.Println("[INFO] Initializing clickstream sync service...")
		clickstreamSyncConfig := &sync.ClickstreamSyncConfig{
			KafkaBrokers: cfg.Generator.KafkaBrokers,
			KafkaTopic:   cfg.Generator.KafkaTopic,
			VeloDB:       velodbClient.GetDB(),
		}
		clickstreamSync, err = sync.NewClickstreamSyncService(clickstreamSyncConfig)
		if err != nil {
			log.Printf("[WARN] Failed to create clickstream sync: %v", err)
		} else if err := clickstreamSync.Start(context.Background()); err != nil {
			log.Printf("[WARN] Failed to start clickstream sync: %v", err)
		} else {
			log.Println("[INFO] Clickstream sync service started")
		}
	} else if velodbClient == nil {
		log.Println("[INFO] Skipping clickstream sync (VeloDB not available)")
	}

	// Initialize ecommerce sync service (Postgres -> VeloDB for Kibana dashboard)
	var ecommerceSync *sync.EcommerceSyncService
	if cfg.Generator.Enabled && velodbClient != nil {
		log.Println("[INFO] Initializing ecommerce sync service...")
		ecommerceSync = sync.NewEcommerceSyncService(postgresClient.GetDB(), velodbClient.GetDB())
		if err := ecommerceSync.Start(context.Background()); err != nil {
			log.Printf("[WARN] Failed to start ecommerce sync: %v", err)
		} else {
			log.Println("[INFO] Ecommerce sync service started (Postgres -> VeloDB)")
		}
	} else if velodbClient == nil {
		log.Println("[INFO] Skipping ecommerce sync (VeloDB not available)")
	}

	startTime := time.Now()

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(velodbClient, postgresClient)
	metricsHandler := handlers.NewMetricsHandler(velodbClient)
	funnelHandler := handlers.NewFunnelHandler(velodbClient)
	productsHandler := handlers.NewProductsHandler(velodbClient)
	// ordersHandler := handlers.NewOrdersHandler(postgresClient) // DEPRECATED: fact_orders no longer used
	clickstreamHandler := handlers.NewClickstreamHandler(velodbClient)
	statusHandler := handlers.NewStatusHandler(velodbClient, postgresClient, startTime)
	// syncHandler REMOVED - legacy fact_orders sync no longer used
	cdcHandler := handlers.NewCDCHandler(velodbClient, postgresClient)
	ecommerceHandler := handlers.NewEcommerceHandler(postgresClient, velodbClient)
	if generatorManager != nil {
		generatorHandler = handlers.NewGeneratorHandler(generatorManager)
		// Set database connections for reset operations
		generatorHandler.SetDatabases(postgresClient.GetDB(), velodbClient.GetDB())
	}

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS middleware
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Routes
	log.Println("[INFO] Registering routes:")

	r.Get("/health", healthHandler.Health)
	log.Println("  GET  /health")

	r.Get("/api/status", statusHandler.Status)
	log.Println("  GET  /api/status")

	r.Get("/metrics", healthHandler.Metrics)
	log.Println("  GET  /metrics")

	r.Get("/api/metrics/revenue", metricsHandler.GetRevenueMetrics)
	log.Println("  GET  /api/metrics/revenue")

	r.Get("/api/funnel", funnelHandler.GetConversionFunnel)
	log.Println("  GET  /api/funnel")

	r.Get("/api/products/top", productsHandler.GetTopProducts)
	log.Println("  GET  /api/products/top")

	// Legacy /api/orders endpoints DEPRECATED - use /api/ecommerce/orders instead
	// These operated on fact_orders table which is no longer used
	// r.Post("/api/orders", ordersHandler.CreateOrder)
	// r.Get("/api/orders", ordersHandler.GetOrder)
	// r.Get("/api/orders/recent", ordersHandler.GetRecentOrders)
	// r.Put("/api/orders/{order_id}/status", ordersHandler.UpdateOrderStatus)
	// r.Delete("/api/orders/{order_id}", ordersHandler.DeleteOrder)
	log.Println("  [DEPRECATED] /api/orders endpoints disabled - use /api/ecommerce/orders")

	r.Get("/api/clickstream/recent", clickstreamHandler.GetRecentClickstream)
	log.Println("  GET  /api/clickstream/recent")

	// Legacy sync routes REMOVED - fact_orders sync no longer used
	// Use /api/ecommerce/* endpoints instead

	// CDC routes
	r.Get("/api/cdc/verify/*", cdcHandler.VerifySync)
	log.Println("  GET  /api/cdc/verify/{order_id}")

	// Ecommerce routes (Kibana integration)
	r.Get("/api/ecommerce/orders/recent", ecommerceHandler.GetRecentOrders)
	log.Println("  GET  /api/ecommerce/orders/recent")
	r.Get("/api/ecommerce/orders/{id}", ecommerceHandler.GetOrderByID)
	log.Println("  GET  /api/ecommerce/orders/{id}")
	r.Put("/api/ecommerce/orders/{id}/status", ecommerceHandler.UpdateOrderStatus)
	log.Println("  PUT  /api/ecommerce/orders/{id}/status")
	r.Delete("/api/ecommerce/orders/{id}", ecommerceHandler.DeleteOrder)
	log.Println("  DELETE /api/ecommerce/orders/{id}")
	r.Get("/api/ecommerce/stats", ecommerceHandler.GetStats)
	log.Println("  GET  /api/ecommerce/stats")
	r.Get("/api/ecommerce/activity-per-minute", ecommerceHandler.GetActivityPerMinute)
	log.Println("  GET  /api/ecommerce/activity-per-minute")

	// POST for creating ecommerce order (uses generator handler for access to service manager)
	if generatorHandler != nil {
		r.Post("/api/ecommerce/orders", generatorHandler.CreateEcommerceOrder)
		log.Println("  POST /api/ecommerce/orders")

		r.Post("/api/ecommerce/orders/batch", generatorHandler.CreateBatchOrders)
		log.Println("  POST /api/ecommerce/orders/batch")
	}

	// Generator routes (if enabled)
	if generatorHandler != nil {
		r.Post("/api/generator/start", generatorHandler.StartGenerator)
		log.Println("  POST /api/generator/start")

		r.Post("/api/generator/stop", generatorHandler.StopGenerator)
		log.Println("  POST /api/generator/stop")

		r.Get("/api/generator/status", generatorHandler.GetStatus)
		log.Println("  GET  /api/generator/status")

		r.Post("/api/generator/spike", generatorHandler.EnableSpike)
		log.Println("  POST /api/generator/spike")

		r.Delete("/api/generator/spike", generatorHandler.DisableSpike)
		log.Println("  DELETE /api/generator/spike")

		r.Post("/api/generator/rate", generatorHandler.SetRate)
		log.Println("  POST /api/generator/rate")

		r.Post("/api/generator/config", generatorHandler.UpdateConfig)
		log.Println("  POST /api/generator/config")

		// Data reset endpoint
		r.Delete("/api/data/reset", generatorHandler.ResetData)
		log.Println("  DELETE /api/data/reset")
	}

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	log.Printf("[INFO] Starting HTTP server on %s", addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[ERROR] Server failed: %v", err)
		}
	}()

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[INFO] Shutting down server...")

	// Stop sync services
	log.Println("[INFO] Stopping sync services...")
	if clickstreamSync != nil {
		if err := clickstreamSync.Stop(); err != nil {
			log.Printf("[ERROR] Failed to stop clickstream sync: %v", err)
		}
	}
	if ecommerceSync != nil {
		if err := ecommerceSync.Stop(); err != nil {
			log.Printf("[ERROR] Failed to stop ecommerce sync: %v", err)
		}
	}

	// Stop generators
	if generatorManager != nil {
		log.Println("[INFO] Stopping data generators...")
		if err := generatorManager.Stop(); err != nil {
			log.Printf("[ERROR] Failed to stop generators: %v", err)
		}
	}

	// Shutdown server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[ERROR] Server forced to shutdown: %v", err)
	}

	log.Println("[INFO] Server stopped")
}
