package main

import (
	"log"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
	"github.com/tinkermc/tinker-agent/internal/api"
)

func main() {
	server := connect.NewServer()
	tinkerv1connect.RegisterAgentServiceHandler(server, &api.Agent{})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true) // TLS comes later

	srv := &http.Server{Addr: ":7122", Handler: mux, Protocols: p}
	log.Printf("tinker-agent %s listening on :7122", api.Version)
	log.Fatal(srv.ListenAndServe())
}
