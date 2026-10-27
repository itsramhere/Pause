package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/paypal"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
)

func Logger() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stdout, nil)) }
func Context() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
func Load(ctx context.Context) (*app.Service, error) {
	// Explicit env wins over the optional local file. The populated .env is never
	// rewritten. Legacy AI_* variables are ignored by the product.
	path := os.Getenv("ENV_FILE")
	if path == "" {
		path = ".env"
	}
	if e := godotenv.Load(path); e != nil && !os.IsNotExist(e) {
		return nil, domain.Fail("ENV_FILE_INVALID", 500)
	}
	c, e := config.Load()
	if e != nil {
		return nil, e
	}
	db, e := store.Open(ctx, c.DatabaseURL)
	if e != nil {
		return nil, domain.Fail("DATABASE_CONFIG_INVALID", 500)
	}
	box, e := secure.NewBox(c.MessageKey)
	if e != nil {
		db.Pool.Close()
		return nil, e
	}
	a := &app.Service{DB: db, Config: c, Clock: domain.RealClock{}, Box: box}
	if c.MLURL != "" && c.ElasticURL != "" {
		a.Investigation, e = investigation.New(c)
		if e != nil {
			db.Pool.Close()
			return nil, e
		}
	}
	if c.PayPalClientID != "" {
		a.Provider = paypal.New(c.PayPalClientID, c.PayPalSecret, c.PayPalWebhookID)
	}
	return a, nil
}
