package protocol

import "time"

const ControlPath = "/_devpreview/agent"
const OpenPath = "/_devpreview/open/"

const (
	TypeWelcome  = "welcome"
	TypeRegister = "register"
	TypeRemove   = "remove"
	TypePing     = "ping"
	TypeAck      = "ack"
	TypeError    = "error"
)

type Message struct {
	Type            string   `json:"type"`
	ClientPublicKey string   `json:"client_public_key,omitempty"`
	PublicURL       string   `json:"public_url,omitempty"`
	Preview         *Preview `json:"preview,omitempty"`
	PreviewID       string   `json:"preview_id,omitempty"`
	Error           string   `json:"error,omitempty"`
}

type Preview struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Port       uint16    `json:"port"`
	ConnToken  string    `json:"conn_token"`
	AccessHash string    `json:"access_hash"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
