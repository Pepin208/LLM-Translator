// Command server runs the LAN web server for LLM-Translator.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	httpOnly := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--http", "--no-tls":
			httpOnly = true
		case "--version", "-v":
			fmt.Println("llmt-server", version)
			return
		case "-h", "--help":
			usage()
			return
		}
	}

	baseDir := config.BASE_DIR
	port := config.ResolvePort()

	srv := web.NewServer(baseDir)

	useSSL := !httpOnly
	if useSSL {
		generated, err := web.EnsureTLSCert(filepath.Join(baseDir, "cert.pem"), filepath.Join(baseDir, "key.pem"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: TLS disabled, could not prepare certificate: %v\n", err)
			useSSL = false
		} else if generated {
			fmt.Println("🔐 Generated a self-signed TLS certificate (cert.pem/key.pem).")
			fmt.Println("   Install cert.pem as a trusted CA on your devices to avoid browser warnings.")
		}
	}
	srv.SetTLS(useSSL)

	token, generated := web.EnsureAuthConfig()
	fmt.Println("=" + repeat("=", 67))
	if generated {
		fmt.Println("  🔐 NEW ACCESS TOKEN (save it now, it will not be shown again):")
	} else {
		fmt.Println("  🔐 ACCESS TOKEN (enter it to log in from any device):")
	}
	if token == "" {
		fmt.Println("      (could not read or create the config file)")
	} else {
		fmt.Printf("      %s\n", token)
	}
	fmt.Println("=" + repeat("=", 67))

	handler := srv.Router()

	protocol := "http"
	if useSSL {
		protocol = "https"
	}

	fmt.Println(repeat("=", 68))
	fmt.Printf("  🚀 LLM Translator %s LAN Server Running (%s)!\n", version, upper(protocol))
	fmt.Printf("  Local Access:   %s://localhost:%d\n", protocol, port)
	fmt.Printf("  LAN Access:     %s://%s:%d\n", protocol, localIP(), port)
	fmt.Printf("  Config file:    %s\n", config.ConfigPath)
	if !useSSL {
		fmt.Println("  💡 (Tip: run without --http to auto-generate a TLS certificate)")
	}
	fmt.Println(repeat("=", 68))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		ErrorLog:          slog.NewLogLogger(srv.Logger().Handler(), slog.LevelWarn),
	}

	go func() {
		var err error
		if useSSL {
			err = httpServer.ListenAndServeTLS(
				filepath.Join(baseDir, "cert.pem"),
				filepath.Join(baseDir, "key.pem"),
			)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\n👋 Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func usage() {
	fmt.Println("llmt-server - LAN web server")
	fmt.Println("")
	fmt.Println("Usage: llmt-server [flags]")
	fmt.Println("")
	fmt.Println("Flags:")
	fmt.Println("  --http         Serve plain HTTP (no TLS)")
	fmt.Println("  --version, -v  Print version and exit")
	fmt.Println("  --help, -h     Show this help")
	fmt.Println("")
	fmt.Println("Environment:")
	fmt.Println("  TRANSLATOR_BASE      Base directory for config and data")
	fmt.Println("  TRANSLATOR_CONFIG    Explicit translator_config.json path")
	fmt.Println("  TRANSLATOR_STATIC    Serve the frontend from this directory")
	fmt.Println("  PORT                 Listen port (default 21346)")
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

func localIP() string {
	return web.LocalIP()
}
