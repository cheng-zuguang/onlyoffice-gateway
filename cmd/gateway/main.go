package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/gateway"
	"github.com/zenmind/onlyoffice-gateway/internal/version"
)

func main() {
	configPath := flag.String("config", "", "optional path to gateway.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("onlyoffice-gateway %s (built %s, commit %s)\n", version.Version, version.BuildTime, version.Commit)
		os.Exit(0)
	}

	loadDotEnv(".env")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	serviceStorePath := os.Getenv("SERVICE_STORE_PATH")
	if serviceStorePath == "" {
		serviceStorePath = "./data/services.json"
	}
	serviceStore, err := admin.NewPersistentServiceStore(serviceStorePath)
	if err != nil {
		log.Fatalf("load service store: %v", err)
	}

	adminUser := os.Getenv("ADMIN_USERNAME")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminPass == "" {
		log.Println("WARNING: ADMIN_PASSWORD not set — admin login will fail")
		log.Println("  Create a .env file:  cp .env.example .env  →  edit ADMIN_PASSWORD")
	}

	handler := newRootHandler(cfg, serviceStore, adminUser, adminPass)

	log.Printf("Gateway %s listening on %s", version.Version, cfg.ListenAddr)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func newRootHandler(cfg *config.Config, serviceStore *admin.InMemoryServiceStore, adminUser, adminPass string) http.Handler {
	gwHandler := gateway.NewHandler(cfg, serviceStore)
	adminMux := admin.NewMux(admin.Opts{
		AdminUsername: adminUser,
		AdminPassword: adminPass,
		JWTSecret:     cfg.JWTSecret,
		Store:         serviceStore,
	})

	mux := http.NewServeMux()
	mux.Handle("/admin/api/", adminMux)
	mux.Handle("/", gwHandler)

	return gateway.LoggingMiddleware(mux)
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if _, err := os.Stat(".env.example"); err == nil {
			log.Println("NOTE: .env file not found, using defaults.")
			log.Println("  Create one:  cp .env.example .env  →  edit as needed")
		}
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if len(val) >= 2 {
			q := val[0]
			if (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
