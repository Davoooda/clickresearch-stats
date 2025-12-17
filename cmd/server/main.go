package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shortid/clickresearch-stats/internal/auth"
	"github.com/shortid/clickresearch-stats/internal/stats"
)

func main() {
	log.Println("Starting ClickResearch Stats server...")

	// Config from env
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// DuckDB store for analytics
	store, err := stats.NewStore(stats.Config{
		S3Endpoint: os.Getenv("S3_ENDPOINT"),
		S3Key:      os.Getenv("S3_KEY"),
		S3Secret:   os.Getenv("S3_SECRET"),
		Bucket:     os.Getenv("S3_BUCKET"),
		Prefix:     os.Getenv("S3_PREFIX"),
	})
	if err != nil {
		log.Fatalf("Failed to create stats store: %v", err)
	}
	defer store.Close()

	// Auth DB for user/project management
	authDB, err := auth.NewDB(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Printf("Warning: Auth DB not available: %v", err)
	} else {
		defer authDB.Close()
	}

	// Handlers
	statsHandler := stats.NewHandler(store)
	authHandler := auth.NewHandler(authDB, os.Getenv("JWT_SECRET"), os.Getenv("WEBHOOK_SECRET"),
		os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET"),
		os.Getenv("GOOGLE_REDIRECT_URL"), os.Getenv("FRONTEND_URL"))

	// Routes
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Stats endpoints
	mux.HandleFunc("/api/stats/overview", statsHandler.HandleOverview)
	mux.HandleFunc("/api/stats/pageviews", statsHandler.HandlePageviews)
	mux.HandleFunc("/api/stats/pages", statsHandler.HandlePages)
	mux.HandleFunc("/api/stats/sources", statsHandler.HandleSources)
	mux.HandleFunc("/api/stats/devices", statsHandler.HandleDevices)
	mux.HandleFunc("/api/stats/geo", statsHandler.HandleGeo)
	mux.HandleFunc("/api/stats/events", statsHandler.HandleEvents)
	mux.HandleFunc("/api/stats/funnel", statsHandler.HandleFunnel)
	mux.HandleFunc("/api/stats/funnel-advanced", statsHandler.HandleFunnelAdvanced)
	mux.HandleFunc("/api/stats/event-breakdown", statsHandler.HandleEventBreakdown)
	mux.HandleFunc("/api/stats/unique-pages", statsHandler.HandleUniquePages)
	mux.HandleFunc("/api/stats/autocapture-events", statsHandler.HandleAutocaptureEvents)

	// Auth endpoints
	if authHandler != nil {
		mux.HandleFunc("/api/auth/register", authHandler.HandleRegister)
		mux.HandleFunc("/api/auth/login", authHandler.HandleLogin)
		mux.HandleFunc("/api/auth/me", authHandler.HandleMe)
		mux.HandleFunc("/api/auth/google", authHandler.HandleGoogleLogin)
		mux.HandleFunc("/api/auth/google/callback", authHandler.HandleGoogleCallback)
		mux.HandleFunc("/api/auth/google/verify", authHandler.HandleGoogleVerify)
		mux.HandleFunc("/api/projects", authHandler.HandleGetProjects)
		mux.HandleFunc("/api/projects/create", authHandler.HandleCreateProject)
		mux.HandleFunc("/api/projects/delete", authHandler.HandleDeleteProject)
		mux.HandleFunc("/api/admin/projects", authHandler.HandleAdminProjects)
		mux.HandleFunc("/api/admin/users", authHandler.HandleAdminUsers)
		mux.HandleFunc("/api/sync/domains", authHandler.HandleSyncDomains)

		// Funnel management endpoints
		mux.HandleFunc("/api/funnels", authHandler.HandleGetFunnels)
		mux.HandleFunc("/api/funnels/create", authHandler.HandleCreateFunnel)
		mux.HandleFunc("/api/funnels/update", authHandler.HandleUpdateFunnel)
		mux.HandleFunc("/api/funnels/delete", authHandler.HandleDeleteFunnel)
	}

	// Middleware: CORS + logging
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// CORS
		origin := r.Header.Get("Origin")
		if origin == "https://shortid.me" || origin == "http://localhost:3000" || origin == "http://localhost:3003" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		mux.ServeHTTP(w, r)

		// Log request
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		server.Close()
	}()

	log.Printf("Stats server starting on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
