package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
	"github.com/tinkermc/tinker-agent/internal/api"
	"github.com/tinkermc/tinker-agent/internal/mc"
)

func main() {
	// Where Minecraft servers live: ~/tinker/servers/<server-id>/server.jar
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "tinker", "servers")
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	manager := mc.NewManager(root)

	server := connect.NewServer()
	tinkerv1connect.RegisterAgentServiceHandler(server, &api.Agent{})
	tinkerv1connect.RegisterServerServiceHandler(server, &api.Servers{Manager: manager})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)

	srv := &http.Server{Addr: ":7122", Handler: mux, Protocols: p}
	log.Printf("tinker-agent %s listening on :7122 (servers in %s)", api.Version, root)
	log.Fatal(srv.ListenAndServe())
}
