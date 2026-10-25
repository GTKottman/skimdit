// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/server"
	"golang.org/x/term"
)

func init() {
	extraCommands["serve"] = cmdServe
	extraCommands["admin"] = cmdAdmin
}

func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("skimdit serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	siteDir := fs.String("site", ".", "site folder (holds config.toml)")
	listen := fs.String("listen", "", "address to listen on (default: server.listen from config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := log.New(stderr, "", log.LstdFlags)
	srv, err := server.New(server.Options{Root: *siteDir, Log: logger, Secret: os.Getenv("SKIMDIT_SECRET")})
	if err != nil {
		return err
	}
	defer srv.Close()
	cfg, err := config.Load(*siteDir)
	if err != nil {
		return err
	}
	addr := cfg.Server.Listen
	if env := os.Getenv("SKIMDIT_LISTEN"); env != "" {
		addr = env
	}
	if *listen != "" {
		addr = *listen
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	logger.Printf("serving %s on %s", cfg.Site.URL, ln.Addr())
	if u := srv.SetupURL(); u != "" {
		logger.Printf("NO ACCOUNT YET. Create it within this run at: %s%s", strings.TrimSuffix(cfg.Site.URL, "/"), strings.TrimPrefix(u, strings.TrimSuffix(cfg.Site.BasePath, "/")))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx, ln)
}

func cmdAdmin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "reset" {
		return usageError{"usage: skimdit admin reset [--site DIR]   (new password, code app off, passkeys removed, everyone signed out)"}
	}
	fs := flag.NewFlagSet("skimdit admin reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	siteDir := fs.String("site", ".", "site folder")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pw, err := readPassword(stdout, "New password: ")
	if err != nil {
		return err
	}
	again, err := readPassword(stdout, "Again: ")
	if err != nil {
		return err
	}
	if pw != again {
		return errors.New("the passwords differ")
	}
	if err := server.ResetAccess(*siteDir, pw); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Done. Sign in with the new password, then add a passkey and a code app again.")
	return nil
}

func readPassword(w io.Writer, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}
