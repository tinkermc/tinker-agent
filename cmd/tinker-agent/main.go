package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
	"github.com/tinkermc/tinker-agent/internal/api"
	"github.com/tinkermc/tinker-agent/internal/mc"
)

func main() {
	// ---- Settings. The installer puts these in /etc/tinker-agent.env.
	// On your Mac, the defaults are used. ----
	home, _ := os.UserHomeDir()
	root := env("TINKER_DATA_DIR", filepath.Join(home, "tinker", "servers"))
	listen := env("TINKER_LISTEN", ":7122")
	token := os.Getenv("TINKER_TOKEN") // the password Dash must send
	java := env("TINKER_JAVA", "java")
	origins := strings.Split(env("TINKER_ALLOWED_ORIGINS",
		"http://tauri.localhost,https://tauri.localhost,tauri://localhost,http://localhost:1420"), ",")

	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	manager := mc.NewManager(root, java)

	server := connect.NewServer()
	tinkerv1connect.RegisterAgentServiceHandler(server, &api.Agent{})
	tinkerv1connect.RegisterServerServiceHandler(server, &api.Servers{Manager: manager})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)

	// Every request goes through: CORS check → password check → your code.
	srv := &http.Server{Addr: listen, Handler: withCORS(origins, withToken(token, mux)), Protocols: p}

	if token == "" {
		log.Printf("WARNING: no TINKER_TOKEN set, so anyone on your network can control this Agent")
	}
	log.Printf("tinker-agent %s listening on %s (servers in %s)", api.Version, listen, root)

	// Run the web server in the background...
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// ...and wait here until someone stops the Agent (Ctrl+C or systemctl stop).
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()

	log.Printf("shutting down: stopping Minecraft servers so worlds are saved…")
	manager.StopAll(60 * time.Second)
	shutdownCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	_ = srv.Shutdown(shutdownCtx)
	log.Printf("bye")
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// withToken rejects any request that doesn't send
// "Authorization: Bearer <token>". Dash adds this header for you.
func withToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Method != http.MethodOptions {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"wrong or missing token"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withCORS lets the Dash app (and only Dash) call the Agent. Dash runs
// inside a web view, and web views block calls to other addresses
// unless the server says "this app is allowed".
func withCORS(allowed []string, next http.Handler) http.Handler {
	ok := map[string]bool{}
	for _, o := range allowed {
		ok[strings.TrimSpace(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		h := w.Header()
		if origin != "" && ok[origin] {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers",
				"Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, Connect-Content-Encoding, Content-Encoding")
		}
		// A "preflight" is the web view asking "may I?" before the real call.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if origin != "" && ok[origin] {
				h.Set("Access-Control-Allow-Methods", "GET, POST")
				h.Set("Access-Control-Allow-Headers",
					"Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, "+
						"Connect-Accept-Encoding, Connect-Content-Encoding, Grpc-Timeout, X-Grpc-Web, X-User-Agent")
				h.Set("Access-Control-Max-Age", "7200")
				// Newer web views also ask before talking to home-network addresses.
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					h.Set("Access-Control-Allow-Private-Network", "true")
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
