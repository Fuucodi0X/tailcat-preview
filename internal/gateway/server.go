package gateway

import (
	"context"
	"crypto/hmac"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/Fuucodi0X/tailcat-preview/internal/protocol"
	"github.com/Fuucodi0X/tailcat-preview/internal/securetoken"
	"github.com/Fuucodi0X/tailcat-preview/internal/session"
	"tailscale.com/types/key"
)

type Config struct {
	ControlToken   string
	PublicURL      string
	InsecureCookie bool
	Logger         *slog.Logger
}

type Server struct {
	config   Config
	key      key.NodePrivate
	registry *Registry
	signer   session.Signer
	logger   *slog.Logger
}

func NewServer(config Config) (*Server, error) {
	if len(config.ControlToken) < 24 {
		return nil, fmt.Errorf("control token must contain at least 24 characters")
	}
	config.PublicURL = strings.TrimRight(config.PublicURL, "/")
	if config.PublicURL == "" {
		return nil, fmt.Errorf("public URL is required")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	clientKey := key.NewNode()
	return &Server{
		config:   config,
		key:      clientKey,
		registry: NewRegistry(clientKey),
		signer:   session.NewSigner(config.ControlToken),
		logger:   config.Logger,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET "+protocol.ControlPath, s.handleAgent)
	mux.HandleFunc("GET "+protocol.OpenPath+"{id}", s.handleOpen)
	mux.HandleFunc("GET /_devpreview", s.handleLanding)
	mux.HandleFunc("GET /_devpreview/", s.handleLanding)
	mux.Handle("/", s)
	return mux
}

func (s *Server) Close() {
	s.registry.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	cookie, err := req.Cookie(session.CookieName)
	if err != nil {
		s.handleLanding(w, req)
		return
	}
	previewID, err := s.signer.Verify(cookie.Value, time.Now())
	if err != nil {
		clearPreviewCookie(w, s.config.InsecureCookie)
		s.handleLanding(w, req)
		return
	}
	preview, err := s.registry.Get(previewID, time.Now())
	if err != nil {
		clearPreviewCookie(w, s.config.InsecureCookie)
		s.renderMessage(w, http.StatusGone, "Preview ended", "The expose command stopped or this preview expired.")
		return
	}
	preview.ServeHTTP(w, req)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleOpen(w http.ResponseWriter, req *http.Request) {
	previewID := req.PathValue("id")
	preview, err := s.registry.Get(previewID, time.Now())
	if err != nil {
		s.renderMessage(w, http.StatusNotFound, "Preview not found", "The link is wrong, expired, or its expose command is no longer running.")
		return
	}
	providedHash := securetoken.Hash(req.URL.Query().Get("key"))
	if !hmac.Equal([]byte(providedHash), []byte(preview.AccessHash)) {
		s.renderMessage(w, http.StatusForbidden, "Preview link rejected", "The access key in this link is not valid.")
		return
	}

	expiresAt := preview.ExpiresAt
	if sessionLimit := time.Now().Add(12 * time.Hour); expiresAt.After(sessionLimit) {
		expiresAt = sessionLimit
	}
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    s.signer.Sign(preview.ID, expiresAt),
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   !s.config.InsecureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, req, "/", http.StatusSeeOther)
}

func (s *Server) handleAgent(w http.ResponseWriter, req *http.Request) {
	if !validBearer(req.Header.Get("Authorization"), s.config.ControlToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.logger.Warn("accept agent WebSocket", "error", err)
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)

	owner, err := securetoken.New(12)
	if err != nil {
		s.logger.Error("generate connection ID", "error", err)
		return
	}
	defer s.registry.RemoveOwner(owner)

	welcome := protocol.Message{
		Type:            protocol.TypeWelcome,
		ClientPublicKey: s.key.Public().String(),
		PublicURL:       s.config.PublicURL,
	}
	if err := writeMessage(req.Context(), c, welcome); err != nil {
		return
	}

	for {
		var message protocol.Message
		if err := wsjson.Read(req.Context(), c, &message); err != nil {
			if websocket.CloseStatus(err) == -1 {
				s.logger.Debug("agent disconnected", "connection", owner, "error", err)
			}
			return
		}
		switch message.Type {
		case protocol.TypeRegister:
			if message.Preview == nil {
				_ = writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeError, Error: "register message is missing a preview"})
				continue
			}
			if err := s.registry.Upsert(owner, *message.Preview); err != nil {
				_ = writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeError, PreviewID: message.Preview.ID, Error: err.Error()})
				continue
			}
			s.logger.Info("preview registered", "id", message.Preview.ID, "name", message.Preview.Name, "port", message.Preview.Port)
			if err := writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeAck, PreviewID: message.Preview.ID}); err != nil {
				return
			}
		case protocol.TypeRemove:
			s.registry.Remove(message.PreviewID)
			if err := writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeAck, PreviewID: message.PreviewID}); err != nil {
				return
			}
		case protocol.TypePing:
			if err := writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeAck}); err != nil {
				return
			}
		default:
			if err := writeMessage(req.Context(), c, protocol.Message{Type: protocol.TypeError, Error: "unknown control message type"}); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleLanding(w http.ResponseWriter, _ *http.Request) {
	s.renderMessage(w, http.StatusOK, "Dev Preview", "Open a preview link produced by the devpreview expose command.")
}

func (s *Server) renderMessage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = messagePage.Execute(w, struct {
		Title   string
		Message string
	}{Title: title, Message: message})
}

func validBearer(header, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return hmac.Equal([]byte(strings.TrimPrefix(header, prefix)), []byte(expected))
}

func writeMessage(ctx context.Context, c *websocket.Conn, message protocol.Message) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, message)
}

func clearPreviewCookie(w http.ResponseWriter, insecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteLaxMode,
	})
}

var messagePage = template.Must(template.New("message").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #101513; color: #edf5f0; }
    main { width: min(34rem, calc(100% - 3rem)); }
    h1 { margin: 0 0 .75rem; font-size: clamp(2rem, 9vw, 4rem); letter-spacing: -.05em; }
    p { margin: 0; color: #a9b8af; font-size: 1.05rem; line-height: 1.6; }
    .mark { width: 2.75rem; height: .35rem; margin-bottom: 1.5rem; border-radius: 1rem; background: #6ee7a8; }
  </style>
</head>
<body><main><div class="mark"></div><h1>{{.Title}}</h1><p>{{.Message}}</p></main></body>
</html>`))
