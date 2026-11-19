package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank"
)

func main() {
	addr := os.Getenv("MOCKBANK_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}

	store := mockbank.NewStore()
	if err := mockbank.SeedDemoData(store); err != nil {
		log.Fatal(err)
	}

	serv := &http.Server{
		Addr:              addr,
		Handler:           mockbank.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Listening on %s", serv.Addr)
	err := serv.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
