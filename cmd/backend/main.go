package main

import (
	"context"
	"github.com/pandada8/pulumid/internal/config"
	"github.com/pandada8/pulumid/internal/httpapi"
	"github.com/pandada8/pulumid/internal/store"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.LUTC)
	c, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	s, e := store.New(c)
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "migrate" {
		if e = s.Migrate(ctx); e != nil {
			log.Fatal(e)
		}
		return
	}
	if e = s.Ready(ctx); e != nil {
		log.Fatal("database not ready: ", e)
	}
	switch cmd {
	case "worker":
		if len(os.Args) > 2 && os.Args[2] == "--once" {
			if e = s.WorkOne(ctx); e != nil {
				log.Fatal(e)
			}
			if e = s.Sweep(ctx); e != nil {
				log.Fatal(e)
			}
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e = s.WorkOne(ctx); e != nil {
					log.Printf("job failed: %T", e)
				}
				if e = s.Sweep(ctx); e != nil {
					log.Printf("expiry failed: %T", e)
				}
			}
		}
	case "serve":
		srv := &http.Server{Addr: c.Listen, Handler: &httpapi.API{Store: s}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			srv.Shutdown(shutdown)
		}()
		log.Printf("listening on %s", c.Listen)
		if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	default:
		log.Fatal("usage: backend serve|worker|migrate")
	}
}
