# Tailcat Preview

Share a web app running on your laptop with a phone, teammate, or client. They open a normal HTTPS link. You keep the app local and do not open a router port.

```text
https://your-gateway.example/_devpreview/open/...?...  ->  localhost:3000
```

Tailcat Preview is useful when you want someone to try a work in progress without deploying that work. It fits quick mobile checks, design reviews, pair debugging, and demos that should disappear when you finish.

## Why use it

- **No app for the visitor.** The preview opens in any current browser.
- **No inbound port on your laptop.** The laptop starts the connection to the hosted gateway.
- **One command per preview.** Point `devpreview` at the port your app already uses.
- **Short-lived access.** Links last four hours by default, stop working when the command exits, and can last no more than 24 hours.
- **Works with live development servers.** HTTP streaming, server-sent events, and WebSocket upgrades pass through the proxy.

This is a better fit than a deployment when the code is unfinished and the laptop should remain the source of truth. It is not a production host, a permanent staging environment, or a way to share sensitive admin tools.

## How it works

```text
Friend's browser
      |
      | HTTPS and a private preview link
      v
Hosted gateway
      |
      | encrypted Tailcat connection
      v
devpreview on your laptop
      |
      | 127.0.0.1:3000
      v
Your existing dev server
```

The repository builds two programs:

- `gateway` is the small public HTTPS entry point. You deploy it once.
- `devpreview` runs beside your local app whenever you want to share a preview.

The gateway reaches the laptop over Tailcat, using a direct WireGuard path when available and a DERP relay otherwise.

## Quick start

You need Go 1.26.5 or newer. Docker 20.10 or newer also works if you do not have Go installed.

### 1. Build the binaries

```bash
git clone https://github.com/Fuucodi0X/tailcat-preview.git
cd tailcat-preview
make build
```

Without a local Go installation:

```bash
docker build --target binaries --output type=local,dest=./bin .
```

### 2. Deploy the gateway once

Generate a control token and save it in a password manager. The gateway and laptop must use the same value.

```bash
openssl rand -hex 32
```

The included [`render.yaml`](render.yaml) runs the gateway on Render:

1. Create a Render Blueprint from this repository.
2. Enter the generated value when Render asks for `DEVPREVIEW_CONTROL_TOKEN`.
3. Wait for `/healthz` to pass.
4. Copy the service URL, such as `https://tailcat-preview-gateway.onrender.com`.

Render passes its public URL to the gateway automatically through `RENDER_EXTERNAL_URL`. The same container can run on another host if you set `DEVPREVIEW_PUBLIC_URL`, `DEVPREVIEW_CONTROL_TOKEN`, and the platform's HTTP port.

### 3. Share a local app

Start your app as usual. If it listens on port 3000, run:

```bash
export DEVPREVIEW_GATEWAY_URL=https://your-service.onrender.com
export DEVPREVIEW_CONTROL_TOKEN=the-token-from-step-2

./bin/devpreview expose --name checkout-redesign 3000
```

`devpreview` first checks that `127.0.0.1:3000` accepts connections. It then prints a link:

```text
https://your-service.onrender.com/_devpreview/open/preview-id?key=browser-access-key
```

Send that full link to the person testing the app. Treat it like a temporary password. Keep the app server and `devpreview` running while they use it. Press `Ctrl+C` to revoke the preview immediately.

To change the lifetime:

```bash
./bin/devpreview expose --name checkout-redesign --ttl 30m 3000
```

## Test everything locally

You can run the gateway, a sample site, and the exposer on one machine before deploying anything.

Create a token and start the gateway:

```bash
export DEVPREVIEW_CONTROL_TOKEN="$(openssl rand -hex 32)"
printf '%s\n' "$DEVPREVIEW_CONTROL_TOKEN" # copy this value for terminal three
export DEVPREVIEW_PUBLIC_URL=http://localhost:8080
export DEVPREVIEW_INSECURE_COOKIE=true
go run ./cmd/gateway
```

In a second terminal, start the included sample page:

```bash
python3 -m http.server --directory testdata/preview 3000
```

In a third terminal, reuse the control token and expose the sample:

```bash
export DEVPREVIEW_GATEWAY_URL=http://localhost:8080
export DEVPREVIEW_CONTROL_TOKEN='paste the value printed in terminal one'
go run ./cmd/devpreview expose --name sample 3000
```

Open the printed link on the same machine. Plain HTTP is only for this local test. A shared gateway should use HTTPS and must not set `DEVPREVIEW_INSECURE_COOKIE`.

## CLI reference

```text
devpreview expose [options] <port>

  --gateway URL         Gateway URL. Defaults to DEVPREVIEW_GATEWAY_URL.
  --control-token TOKEN Shared token. Defaults to DEVPREVIEW_CONTROL_TOKEN.
  --name NAME           Label used in logs. Defaults to the current directory.
  --local-host HOST     Dev server host. Defaults to 127.0.0.1.
  --ttl DURATION        Link lifetime. Defaults to 4h, maximum 24h.
  --verbose             Print Tailcat networking logs.
```

Examples:

```bash
# Vite on its common port
./bin/devpreview expose --name web-ui 5173

# A preview that expires after one hour
./bin/devpreview expose --ttl 1h --name review 8081
```

## Security model

The control token authenticates `devpreview` to the gateway. It must never appear in source control or in a preview URL.

Each `expose` command creates a new ephemeral Tailcat identity, preview ID, and 256-bit browser access key. The laptop accepts only the gateway's current Tailcat public key. The gateway stores a SHA-256 hash of the browser access key, not the key itself.

On first use, the preview link creates a signed, HTTP-only browser cookie and redirects to a URL without the access key. The gateway terminates HTTPS and can read the proxied application traffic. Do not expose production data, credential-bearing admin pages, or anything the gateway operator must not see.

Stopping `devpreview` removes its registration and closes the Tailcat server. Registrations live in gateway memory, and the laptop restores an active registration after a gateway restart.

## Current limits

- One `devpreview` process exposes one local port.
- The laptop, local app, and `devpreview` process must stay online.
- Free Render services can sleep, so the first request may take longer.
- Absolute redirects to localhost are rewritten. Apps with unusual external URL generation may still need a public-host setting.
- Apps that validate the `Host` header may need to allow the gateway hostname in development.
- Tailcat's public DERP fallback is rate-limited and has no uptime guarantee.
- The gateway has not been load-tested on Render's 512 MB free instance.

## Development

```bash
make test    # go test ./...
make build   # build gateway and devpreview into bin/
make docker  # build the gateway container
make tidy    # update Go module metadata
```

Tailcat Preview currently pins Tailcat v0.3.0 because its API and wire format are still unstable. Upgrade the gateway and CLI together, then test both direct and relay connections.
