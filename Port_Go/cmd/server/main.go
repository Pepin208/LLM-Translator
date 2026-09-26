// Command server runs the LAN web server for LLM Subtitle Translator (Go port).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"translate_llm/internal/config"
	"translate_llm/internal/web"
)

const port = config.DefaultPort

func main() {
	baseDir := config.BASE_DIR

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

	srv := web.NewServer(baseDir)
	handler := srv.Router()

	useSSL := srv.UseSSL()
	protocol := "http"
	if useSSL {
		protocol = "https"
	}

	fmt.Println(repeat("=", 68))
	fmt.Printf("  🚀 LLM Subtitle Translator (Go) LAN Server Running (%s)!\n", upper(protocol))
	fmt.Printf("  Local Access:   %s://localhost:%d\n", protocol, port)
	fmt.Printf("  LAN Access:     %s://%s:%d\n", protocol, localIP(), port)
	fmt.Printf("  Config file:    %s\n", config.ConfigPath)
	if !useSSL {
		fmt.Println("  💡 (Tip: place 'cert.pem' and 'key.pem' in the project root to enable HTTPS)")
	}
	fmt.Println(repeat("=", 68))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		var err error
		if useSSL {
			err = httpServer.ListenAndServeTLS(
				baseDir+"/cert.pem",
				baseDir+"/key.pem",
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
