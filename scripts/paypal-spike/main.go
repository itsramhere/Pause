package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := flag.String("root", "../..", "repository root (run from scripts/paypal-spike)")
	flag.Parse()
	command := "check"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	c, err := getConfig(*root)
	if err == nil {
		switch command {
		case "check":
			var st *diskStore
			st, err = openStore(c.EvidenceDir)
			if err == nil {
				inventory := c.inventory()
				inventory["recorded_at"] = time.Now().UTC()
				inventory["provider_environment"] = st.data.Environment
				err = atomicJSON(filepath.Join(c.EvidenceDir, "configuration-inventory.json"), inventory)
				if err == nil {
					_ = json.NewEncoder(os.Stdout).Encode(inventory)
					err = c.requirePayPal()
				}
			}
		case "serve":
			err = serve(c)
		case "ai":
			c, err = c.legacyAIConfig()
			if err != nil {
				break
			}
			if err = os.MkdirAll(c.EvidenceDir, 0700); err == nil {
				client := &http.Client{Timeout: c.AITimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
				record, callErr := runAI(context.Background(), c, client)
				err = atomicJSON(filepath.Join(c.EvidenceDir, "ai-"+newID()+".json"), record)
				if err == nil {
					_ = json.NewEncoder(os.Stdout).Encode(record)
					err = callErr
				}
			}
		default:
			err = fmt.Errorf("unknown command; use check, serve, or ai")
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
