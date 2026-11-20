package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank"
	"github.com/2inxs/openbanking-service/internal/mockbank/service"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

func main() {
	addr := os.Getenv("MOCKBANK_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}

	token := os.Getenv("MOCKBANK_DEMO_TOKEN")
	if token == "" {
		log.Fatal("MOCKBANK_DEMO_TOKEN is required")
	}

	dataStore := store.NewStore()
	if err := store.SeedDemoData(dataStore); err != nil {
		log.Fatal(err)
	}
	svc := service.New(dataStore)

	auth := mockbank.AuthConfig{
		DemoToken:    token,
		DemoClientID: "cl-001",
	}

	serv := &http.Server{
		Addr:              addr,
		Handler:           mockbank.NewHandler(svc, auth),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Listening on %s", serv.Addr)
	err := serv.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
