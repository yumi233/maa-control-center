package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yumi233/maa-control-center/internal/api"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/storage"
	"github.com/yumi233/maa-control-center/internal/ui"
	"github.com/yumi233/maa-control-center/internal/vault"
)

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func main() {
	configPath := flag.String("config-file", "", "private JSON configuration; not tracked in Git")
	addr := flag.String("listen", env("MAACC_LISTEN", "127.0.0.1:8421"), "HTTP(S) listen address")
	data := flag.String("db", env("MAACC_DB", "data/control-center.db"), "SQLite database path")
	flag.Parse()
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatal("cannot read private configuration")
		}
		var settings map[string]string
		if json.Unmarshal(b, &settings) != nil {
			log.Fatal("invalid private configuration")
		}
		allowed := map[string]bool{"MAACC_PUBLIC_URL": true, "MAACC_ALLOWED_ORIGINS": true, "MAACC_LISTEN": true, "MAACC_DB": true, "MAACC_ADMIN_PASSWORD": true, "MAACC_AGENT_ID": true, "MAACC_AGENT_TOKEN": true, "MAACC_TLS_CERT": true, "MAACC_TLS_KEY": true, "MAACC_PRIMARY_NAME": true, "MAACC_SECONDARY_NAME": true, "MAACC_BINDINGS_FILE": true}
		for k, v := range settings {
			if !allowed[k] {
				log.Fatal("unsupported private configuration key")
			}
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
		if *addr == "127.0.0.1:8421" {
			*addr = env("MAACC_LISTEN", *addr)
		}
		if *data == "data/control-center.db" {
			*data = env("MAACC_DB", *data)
		}
	}
	publicURL := env("MAACC_PUBLIC_URL", "http://127.0.0.1:8421")
	origins, err := api.ParseOrigins(append([]string{publicURL}, strings.Split(os.Getenv("MAACC_ALLOWED_ORIGINS"), ",")...))
	if err != nil {
		log.Fatal("invalid public URL or allowed origins: ", err)
	}
	cert, key := os.Getenv("MAACC_TLS_CERT"), os.Getenv("MAACC_TLS_KEY")
	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") && cert == "" && os.Getenv("MAACC_ALLOW_LAN_HTTP") != "true" {
		log.Fatal("non-loopback listener requires TLS; MAACC_ALLOW_LAN_HTTP=true is for isolated test networks only")
	}
	store, err := storage.Open(*data, os.Getenv("MAACC_ADMIN_PASSWORD"), os.Getenv("MAACC_PRIMARY_NAME"), os.Getenv("MAACC_SECONDARY_NAME"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.DB.Close()
	if path := os.Getenv("MAACC_BINDINGS_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatal("read bindings: ", err)
		}
		var bindings []model.Binding
		if err = json.Unmarshal(b, &bindings); err != nil {
			log.Fatal("invalid bindings file")
		}
		for _, binding := range bindings {
			if err = store.Bind(binding); err != nil {
				log.Printf("binding deferred for %s: instance not yet discovered", binding.AccountID)
			}
		}
	}
	secrets, err := vault.Open(filepath.Join(filepath.Dir(*data), "integrations.key"))
	if err != nil {
		log.Fatal("cannot initialize encrypted integration storage")
	}
	s := api.New(store, api.Config{PublicURL: publicURL, AgentID: env("MAACC_AGENT_ID", "windows-agent"), AgentToken: os.Getenv("MAACC_AGENT_TOKEN"), Assets: ui.Files(), Vault: secrets, AllowedOrigins: origins})
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go s.Worker(workerCtx)
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		stopWorker()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	log.Printf("MAA Control Center 0.5.0 | reviewed configuration | listen=%s", *addr)
	if cert != "" {
		err = srv.ListenAndServeTLS(cert, key)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
