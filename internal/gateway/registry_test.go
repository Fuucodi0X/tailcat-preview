package gateway

import (
	"net/http"
	"testing"

	"github.com/Fuucodi0X/tailcat-preview/internal/session"
)

func TestStripGatewayCookiePreservesAppCookies(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "app_session", Value: "keep"})
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "remove"})

	stripGatewayCookie(req)
	cookies := req.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "app_session" || cookies[0].Value != "keep" {
		t.Fatalf("cookies after stripping = %#v", cookies)
	}
}

func TestStripGatewaySetCookiePreservesAppCookies(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "app_session=keep; Path=/")
	header.Add("Set-Cookie", session.CookieName+"=replace; Path=/")

	stripGatewaySetCookie(header)
	values := header.Values("Set-Cookie")
	if len(values) != 1 || values[0] != "app_session=keep; Path=/" {
		t.Fatalf("Set-Cookie after stripping = %#v", values)
	}
}
