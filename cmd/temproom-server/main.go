package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"temproom/internal/server"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := server.NewServer()
	defer srv.Close()
	addr := ":" + port

	fmt.Println("==================================================")
	fmt.Println("  TempRoom Cloud Control & Signaling Server")
	fmt.Printf("  Listening on http://0.0.0.0:%s\n", port)
	fmt.Println("  Healthcheck : GET  /health")
	fmt.Println("  Signaling WS: GET  /ws")
	fmt.Println("  REST API    : POST /api/v1/rooms/*")
	fmt.Println("==================================================")

	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
