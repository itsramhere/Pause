package main

import (
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/investigation"
	"os"
)

func main() {
	name := flag.String("index", investigation.HistoryIndex, "fresh compatible private index (also use to safely rebuild/rollback)")
	retry := flag.Bool("retry-failed", false, "retry failed projection cleanup after dependency recovery")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	log := bootstrap.Logger()
	s, err := bootstrap.Load(ctx)
	if err != nil {
		log.Error("history_setup_failed", "reason_code", "CONFIGURATION_ERROR")
		os.Exit(1)
	}
	defer s.DB.Pool.Close()
	if *retry {
		if err = s.RetryHistoryCleanup(ctx); err != nil {
			log.Error("history_retry_failed", "reason_code", "DATABASE_UNAVAILABLE")
			os.Exit(1)
		}
	}
	if err = s.ReindexHistory(ctx, *name); err != nil {
		log.Error("history_setup_failed", "reason_code", "PRIVATE_REINDEX_FAILED")
		os.Exit(1)
	}
	log.Info("history_index_ready", "index", *name)
}
