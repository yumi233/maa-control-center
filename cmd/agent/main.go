package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/agent"
	"github.com/yumi233/maa-control-center/internal/model"
)

type values []string

func (v *values) String() string     { return strings.Join(*v, ",") }
func (v *values) Set(s string) error { *v = append(*v, s); return nil }
func main() {
	configPath := flag.String("config-file", "", "private agent JSON configuration")
	var roots, allowed values
	flag.Var(&roots, "maa-dir", "explicit existing MAA installation; repeat for multiple installations")
	flag.Var(&allowed, "allow-read-dir", "additional allowed schedule directories; repeat as necessary")
	server := flag.String("server", os.Getenv("MAACC_SERVER"), "NAS HTTPS URL")
	id := flag.String("id", "windows-agent", "paired agent ID")
	once := flag.Bool("once", false, "one read-only heartbeat")
	ca := flag.String("ca", "", "trusted private CA certificate")
	interval := flag.Duration("interval", 25*time.Second, "heartbeat interval (minimum 10 seconds)")
	out := flag.String("snapshot", "", "write a local, redacted discovery snapshot instead of connecting")
	flag.Parse()
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatal("cannot read agent configuration")
		}
		var cfg struct {
			Server        string   `json:"server"`
			Token         string   `json:"token"`
			AgentID       string   `json:"agent_id"`
			MAADirs       []string `json:"maa_dirs"`
			AllowReadDirs []string `json:"allow_read_dirs"`
			CA            string   `json:"ca"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			log.Fatal("invalid agent configuration")
		}
		if *server == "" {
			*server = cfg.Server
		}
		if *ca == "" {
			*ca = cfg.CA
		}
		if len(roots) == 0 {
			roots = cfg.MAADirs
		}
		if len(allowed) == 0 {
			allowed = cfg.AllowReadDirs
		}
		if cfg.AgentID != "" && *id == "windows-agent" {
			*id = cfg.AgentID
		}
		if os.Getenv("MAACC_AGENT_TOKEN") == "" {
			os.Setenv("MAACC_AGENT_TOKEN", cfg.Token)
		}
	}
	if len(roots) == 0 {
		log.Fatal("provide at least one --maa-dir; automatic whole-disk scanning is disabled")
	}
	if *interval < 10*time.Second {
		log.Fatal("minimum heartbeat interval is 10s")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return fmt.Errorf("redirects disabled") }}
	if *ca != "" {
		b, err := os.ReadFile(*ca)
		if err != nil {
			log.Fatal(err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(b) {
			log.Fatal("invalid CA certificate")
		}
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	if *out == "" {
		u, err := url.Parse(*server)
		if err != nil || u.Host == "" {
			log.Fatal("valid --server required")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
			log.Fatal("agent requires HTTPS outside loopback")
		}
		if len(os.Getenv("MAACC_AGENT_TOKEN")) < 32 {
			log.Fatal("paired MAACC_AGENT_TOKEN required")
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	host, _ := os.Hostname()
	for {
		h := model.Heartbeat{AgentID: *id, Hostname: host, OS: runtime.GOOS, Version: "0.1.0", Instances: []model.Instance{}}
		for _, root := range roots {
			h.Instances = append(h.Instances, agent.Discover(root, allowed))
		}
		data, err := json.Marshal(h)
		if err != nil {
			log.Fatal(err)
		}
		if *out != "" {
			if err = os.WriteFile(*out, data, 0600); err != nil {
				log.Fatal(err)
			}
			log.Print("read-only discovery snapshot written")
			return
		}
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(*server, "/")+"/api/v1/agent/heartbeat", bytes.NewReader(data))
		if err != nil {
			log.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+os.Getenv("MAACC_AGENT_TOKEN"))
		res, err := client.Do(req)
		if err != nil {
			log.Print("heartbeat failed; no execution or configuration changes performed")
		} else {
			io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			res.Body.Close()
			if res.StatusCode != 200 {
				err = fmt.Errorf("heartbeat rejected: HTTP %d", res.StatusCode)
				log.Print(err)
			} else {
				log.Printf("read-only heartbeat accepted; installations=%d", len(h.Instances))
			}
		}
		if *once {
			if err != nil {
				os.Exit(1)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}
