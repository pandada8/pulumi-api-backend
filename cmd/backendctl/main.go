package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/pandada8/pulumid/internal/config"
	"github.com/pandada8/pulumid/internal/core"
	"github.com/pandada8/pulumid/internal/store"
	"log"
	"os"
	"path/filepath"
	"regexp"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) < 2 {
		log.Fatal("usage: backendctl bootstrap|token create|token revoke|member set|restore-generation")
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	if cmd == "token" || cmd == "member" {
		if len(args) == 0 {
			log.Fatal("missing subcommand")
		}
		cmd += " " + args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	org := f.String("org", "demo", "")
	user := f.String("user", "admin", "")
	out := f.String("out", "", "")
	tokenFile := f.String("token-file", "", "")
	id := f.String("id", "", "")
	role := f.String("role", "reader", "")
	write := f.Bool("write", false, "")
	decrypt := f.Bool("decrypt", false, "")
	f.Parse(args)
	if cmd == "bootstrap" {
		path := os.Getenv("BACKEND_MASTER_KEY_FILE")
		if path != "" {
			if _, e := os.Stat(path); os.IsNotExist(e) {
				if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
					log.Fatal(e)
				}
				if e = os.WriteFile(path, core.Random(), 0600); e != nil {
					log.Fatal(e)
				}
			}
		}
	}
	c, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	s, e := store.New(c)
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	ctx := context.Background()
	switch cmd {
	case "bootstrap":
		if !regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`).MatchString(*org) {
			log.Fatal("invalid org")
		}
		if *tokenFile == "" {
			log.Fatal("--token-file required")
		}
		if _, e = os.Stat(*tokenFile); e == nil {
			log.Fatal("token file already exists; bootstrap not repeated")
		}
		e = s.Bootstrap(ctx, *org, *user, *tokenFile)
	case "token create":
		if *out == "" {
			log.Fatal("--out required")
		}
		if _, e = os.Stat(*out); e == nil {
			log.Fatal("token file exists")
		}
		e = s.TokenCreate(ctx, *user, *write, *decrypt, *out)
	case "token revoke":
		e = s.Revoke(ctx, *id)
	case "member set":
		if *role != "reader" && *role != "writer" && *role != "admin" {
			log.Fatal("invalid role")
		}
		e = s.Member(ctx, *org, *user, *role, *decrypt)
	case "restore-generation":
		e = s.RestoreGeneration(ctx)
		if e == nil {
			e = s.Sweep(ctx)
		}
	default:
		e = fmt.Errorf("unknown command")
	}
	if e != nil {
		log.Fatal(e)
	}
}
