package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/gateway"
	"github.com/zenmind/onlyoffice-gateway/internal/version"
)

func main() {
	configPath := flag.String("config", "gateway.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("onlyoffice-gateway %s (built %s, commit %s)\n", version.Version, version.BuildTime, version.Commit)
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	handler := gateway.NewHandler(cfg)

	log.Printf("Gateway %s listening on %s", version.Version, cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
