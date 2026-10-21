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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yumi233/maa-control-center/internal/agent"
	"github.com/yumi233/maa-control-center/internal/configsync"
	"github.com/yumi233/maa-control-center/internal/desktop"
	"github.com/yumi233/maa-control-center/internal/jobrunner"
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
	runner := configsync.Runner{}
	executionEnabled := false
	stateDir := ""
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatal("cannot read agent configuration")
		}
		var cfg struct {
			Server           string            `json:"server"`
			Token            string            `json:"token"`
			AgentID          string            `json:"agent_id"`
			MAADirs          []string          `json:"maa_dirs"`
			AllowReadDirs    []string          `json:"allow_read_dirs"`
			CA               string            `json:"ca"`
			AllowConfigWrite bool              `json:"allow_config_write"`
			AllowTaskControl bool              `json:"allow_task_control"`
			StateDir         string            `json:"state_dir"`
			ProfileRoles     map[string]string `json:"profile_roles"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			log.Fatal("invalid agent configuration")
		}
		runner.Enabled = cfg.AllowConfigWrite
		executionEnabled = cfg.AllowTaskControl
		runner.Roles = cfg.ProfileRoles
		stateDir = cfg.StateDir
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
	if stateDir == "" && *configPath != "" {
		stateDir = filepath.Join(filepath.Dir(*configPath), "agent-state")
	}
	runner.Roots = roots
	runner.Allowed = allowed
	runner.BackupDir = filepath.Join(stateDir, "configuration-backups")
	controller := desktop.Controller{}
	if executionEnabled {
		runner.Prepare = controller.Prepare
	}
	tasks := &jobrunner.Runner{Enabled: executionEnabled, Roots: roots, Allowed: allowed, Roles: runner.Roles, StateDir: filepath.Join(stateDir, "jobs"), LockDir: runner.BackupDir, Control: controller}
	if !*once && *out == "" {
		go func() {
			for {
				syncCommands(ctx, client, *server, &runner)
				syncJobs(ctx, client, *server, tasks)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
	var watchSince time.Time
	if stateDir != "" {
		var e error
		watchSince, e = agent.WatchSince(filepath.Join(stateDir, "completion-watch.json"))
		if e != nil {
			log.Print("completion watcher unavailable; configuration unchanged")
		}
	}
	for {
		h := model.Heartbeat{AgentID: *id, Hostname: host, OS: runtime.GOOS, Version: "0.4.0", Instances: []model.Instance{}}
		for _, root := range roots {
			inst := agent.Discover(root, allowed)
			inst.ConfigurationWrite = runner.Enabled && len(runner.Roles) > 0
			inst.TaskExecution = executionEnabled && len(runner.Roles) > 0
			h.Instances = append(h.Instances, inst)
			if !watchSince.IsZero() {
				h.Completions = append(h.Completions, agent.Completions(root, inst.ID, watchSince)...)
			}
		}
		if len(h.Completions) > 20 {
			h.Completions = h.Completions[len(h.Completions)-20:]
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
				log.Printf("heartbeat accepted; installations=%d", len(h.Instances))

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

func syncCommands(ctx context.Context, client *http.Client, server string, runner *configsync.Runner) {
	req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(server, "/")+"/api/v1/agent/config/commands", nil)
	if e != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("MAACC_AGENT_TOKEN"))
	res, e := client.Do(req)
	if e != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return
	}
	var commands []configsync.Command
	if json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&commands) != nil || len(commands) > 4 {
		return
	}
	for _, cmd := range commands {
		receipt := runner.Execute(cmd)

		b, e := json.Marshal(receipt)
		if e != nil {
			continue
		}
		r, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(server, "/")+"/api/v1/agent/config/receipts", bytes.NewReader(b))
		if e != nil {
			continue
		}
		r.Header.Set("Authorization", "Bearer "+os.Getenv("MAACC_AGENT_TOKEN"))
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			log.Print("configuration receipt pending; local journal prevents replay")
			continue
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		log.Printf("configuration operation state=%s receipt_http=%d", receipt.State, response.StatusCode)
	}
}

func syncJobs(ctx context.Context, client *http.Client, server string, runner *jobrunner.Runner) {
	req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(server, "/")+"/api/v1/agent/jobs/commands", nil)
	if e != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("MAACC_AGENT_TOKEN"))
	res, e := client.Do(req)
	if e != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return
	}
	var commands []jobrunner.Command
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&commands) != nil || len(commands) > 1 {
		return
	}
	for _, command := range commands {
		receipt := runner.Step(command)
		b, _ := json.Marshal(receipt)
		req, e = http.NewRequestWithContext(ctx, "POST", strings.TrimRight(server, "/")+"/api/v1/agent/jobs/receipts", bytes.NewReader(b))
		if e != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv("MAACC_AGENT_TOKEN"))
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		if e != nil {
			log.Print("execution receipt pending; durable journal prevents duplicate launch")
			return
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		log.Printf("execution state=%s receipt_http=%d", receipt.State, response.StatusCode)
	}
}
