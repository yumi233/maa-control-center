// bot-bind provisions one narrowly scoped group connection into a private file.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/yumi233/maa-control-center/internal/bot"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	dbPath := flag.String("db", "data/control-center.db", "existing migrated database")
	owner := flag.String("owner", "admin", "existing account owner ID")
	account := flag.String("account", "", "account ID")
	umo := flag.String("umo", "", "exact group or private UMO")
	uid := flag.String("uid", "", "initial operator UID")
	label := flag.String("label", "初始操作者", "initial operator label")
	output := flag.String("output", "", "new private connection JSON file")
	base := flag.String("server", "", "HTTPS control center URL")
	ca := flag.String("ca-file", "", "CA path as seen by AstrBot")
	flag.Parse()
	if *output == "" || !strings.HasPrefix(*base, "https://") {
		return fmt.Errorf("output and HTTPS server are required")
	}
	if e := os.MkdirAll(filepath.Dir(*output), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("private output already exists or cannot be created")
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(*output)
		}
	}()
	db, e := sql.Open("sqlite", *dbPath)
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA busy_timeout=5000;PRAGMA foreign_keys=ON")
	var name string
	if e = db.QueryRow("SELECT name FROM accounts WHERE id=? AND owner_id=?", *account, *owner).Scan(&name); e != nil {
		return fmt.Errorf("account does not belong to owner")
	}
	b, key, e := bot.Bind(db, bot.Binding{OwnerID: *owner, AccountID: *account, UMO: *umo, Members: []bot.Member{{UID: *uid, Label: *label, Role: "operator"}}})
	if e != nil {
		return e
	}
	data := map[string]any{"base_url": strings.TrimRight(*base, "/"), "ca_file": *ca, "routes": []any{map[string]string{"account_id": b.AccountID, "label": name, "umo": b.UMO, "token": key}}}
	raw, _ := json.MarshalIndent(data, "", "  ")
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	if e != nil {
		db.Exec("UPDATE bot_bindings SET enabled=0 WHERE id=?", b.ID)
		return fmt.Errorf("private file write failed; binding revoked")
	}
	complete = true
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"binding_id": b.ID, "account_id": b.AccountID, "configured": true, "secret_printed": false})
}
