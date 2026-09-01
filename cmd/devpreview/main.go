package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/Fuucodi0X/tailcat-preview/internal/protocol"
	"github.com/Fuucodi0X/tailcat-preview/internal/securetoken"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

type exposeConfig struct {
	GatewayURL   string
	ControlToken string
	Name         string
	LocalHost    string
	Port         uint16
	TTL          time.Duration
	Verbose      bool
}

type exposer struct {
	config    exposeConfig
	server    *tailcat.Server
	preview   protocol.Preview
	accessKey string
	lastLink  string
	logger    *log.Logger
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "devpreview:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] != "expose" {
		printUsage()
		return errors.New("expected the expose command")
	}

	flags := flag.NewFlagSet("expose", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	defaultName, _ := os.Getwd()
	defaultName = filepath.Base(defaultName)
	gatewayURL := flags.String("gateway", os.Getenv("DEVPREVIEW_GATEWAY_URL"), "gateway base URL")
	controlToken := flags.String("control-token", os.Getenv("DEVPREVIEW_CONTROL_TOKEN"), "shared gateway control token")
	name := flags.String("name", defaultName, "preview name")
	localHost := flags.String("local-host", "127.0.0.1", "host where the dev server listens")
	ttl := flags.Duration("ttl", 4*time.Hour, "preview lifetime, up to 24h")
	verbose := flags.Bool("verbose", false, "show Tailcat debug logs")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one local development-server port")
	}
	portValue, err := strconv.ParseUint(flags.Arg(0), 10, 16)
	if err != nil || portValue == 0 {
		return fmt.Errorf("invalid port %q", flags.Arg(0))
	}
	if strings.TrimSpace(*gatewayURL) == "" {
		return errors.New("gateway URL is required through --gateway or DEVPREVIEW_GATEWAY_URL")
	}
	if len(*controlToken) < 24 {
		return errors.New("control token must contain at least 24 characters")
	}
	if *ttl <= 0 || *ttl > 24*time.Hour {
		return errors.New("TTL must be greater than zero and no more than 24h")
	}

	config := exposeConfig{
		GatewayURL:   strings.TrimRight(*gatewayURL, "/"),
		ControlToken: *controlToken,
		Name:         strings.TrimSpace(*name),
		LocalHost:    *localHost,
		Port:         uint16(portValue),
		TTL:          *ttl,
		Verbose:      *verbose,
	}
	if config.Name == "" {
		return errors.New("preview name cannot be empty")
	}
	if err := checkLocalServer(config.LocalHost, config.Port); err != nil {
		return err
	}

	previewID, err := securetoken.New(9)
	if err != nil {
		return err
	}
	accessKey, err := securetoken.New(32)
	if err != nil {
		return err
	}
	now := time.Now()
	e := &exposer{
		config:    config,
		accessKey: accessKey,
		logger:    log.New(os.Stderr, "devpreview: ", log.LstdFlags),
		preview: protocol.Preview{
			ID:         previewID,
			Name:       config.Name,
			Port:       config.Port,
			AccessHash: securetoken.Hash(accessKey),
			CreatedAt:  now.UTC(),
			ExpiresAt:  now.Add(config.TTL).UTC(),
		},
	}

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(baseCtx, e.preview.ExpiresAt)
	defer cancel()
	defer e.close()

	return e.run(ctx)
}

func (e *exposer) run(ctx context.Context) error {
	backoff := time.Second
	for {
		if err := e.controlSession(ctx); err != nil && ctx.Err() == nil {
			e.logger.Printf("gateway connection lost: %v", err)
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		backoff = time.Second
	}
}

func (e *exposer) controlSession(ctx context.Context) error {
	controlURL, err := url.Parse(e.config.GatewayURL)
	if err != nil {
		return fmt.Errorf("parse gateway URL: %w", err)
	}
	controlURL.Path = protocol.ControlPath
	controlURL.RawQuery = ""
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+e.config.ControlToken)
	c, response, err := websocket.Dial(ctx, controlURL.String(), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect to gateway: HTTP %d: %w", response.StatusCode, err)
		}
		return fmt.Errorf("connect to gateway: %w", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)

	welcomeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var welcome protocol.Message
	if err := wsjson.Read(welcomeCtx, c, &welcome); err != nil {
		return fmt.Errorf("read gateway welcome: %w", err)
	}
	if welcome.Type != protocol.TypeWelcome {
		return fmt.Errorf("gateway sent %q instead of a welcome", welcome.Type)
	}
	var gatewayKey key.NodePublic
	if err := gatewayKey.UnmarshalText([]byte(welcome.ClientPublicKey)); err != nil {
		return fmt.Errorf("parse gateway Tailcat key: %w", err)
	}
	if err := e.ensureTailcatServer(gatewayKey); err != nil {
		return err
	}

	if err := writeControl(ctx, c, protocol.Message{Type: protocol.TypeRegister, Preview: &e.preview}); err != nil {
		return fmt.Errorf("register preview: %w", err)
	}

	messages := make(chan protocol.Message)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var message protocol.Message
			if err := wsjson.Read(ctx, c, &message); err != nil {
				readErrors <- err
				return
			}
			select {
			case messages <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = writeControl(context.Background(), c, protocol.Message{Type: protocol.TypeRemove, PreviewID: e.preview.ID})
			return nil
		case err := <-readErrors:
			return err
		case message := <-messages:
			switch message.Type {
			case protocol.TypeAck:
				if message.PreviewID == e.preview.ID {
					e.printLink(welcome.PublicURL)
				}
			case protocol.TypeError:
				return errors.New(message.Error)
			}
		case <-heartbeat.C:
			if err := writeControl(ctx, c, protocol.Message{Type: protocol.TypePing}); err != nil {
				return err
			}
		}
	}
}

func (e *exposer) ensureTailcatServer(gatewayKey key.NodePublic) error {
	if e.server != nil {
		e.server.AddAllowedClient(gatewayKey)
		return nil
	}
	server := &tailcat.Server{
		AllowedClients: []key.NodePublic{gatewayKey},
		OnTCP: func(port uint16) func(net.Conn) {
			if port != e.config.Port {
				return nil
			}
			return func(remote net.Conn) {
				local, err := net.DialTimeout("tcp", net.JoinHostPort(e.config.LocalHost, strconv.Itoa(int(e.config.Port))), 5*time.Second)
				if err != nil {
					e.logger.Printf("connect to local dev server: %v", err)
					_ = remote.Close()
					return
				}
				tailcat.ProxyConns(remote, local)
			}
		},
	}
	if !e.config.Verbose {
		server.Logf = func(string, ...any) {}
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start Tailcat server: %w", err)
	}
	e.server = server
	e.preview.ConnToken = string(server.ConnBlob())
	return nil
}

func (e *exposer) printLink(publicURL string) {
	publicURL = strings.TrimRight(publicURL, "/")
	link := publicURL + protocol.OpenPath + url.PathEscape(e.preview.ID) + "?key=" + url.QueryEscape(e.accessKey)
	if link == e.lastLink {
		return
	}
	e.lastLink = link
	fmt.Fprintln(os.Stdout, link)
	e.logger.Printf("preview %q is available until %s", e.preview.Name, e.preview.ExpiresAt.Local().Format(time.RFC3339))
}

func (e *exposer) close() {
	if e.server != nil {
		_ = e.server.Close()
	}
}

func writeControl(ctx context.Context, c *websocket.Conn, message protocol.Message) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c, message)
}

func checkLocalServer(host string, port uint16) error {
	address := net.JoinHostPort(host, strconv.Itoa(int(port)))
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return fmt.Errorf("local development server is not reachable at %s: %w", address, err)
	}
	_ = conn.Close()
	return nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: devpreview expose [options] <port>")
}
