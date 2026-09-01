# Tailcat Preview

Tailcat Preview makes a development server on your laptop available in your phone's browser. The laptop keeps its ports closed to the public internet. A small gateway accepts HTTPS requests and reaches the laptop through an encrypted Tailcat connection.

This repository contains the first working slice:

- `devpreview expose` runs beside a local development server.
- `gateway` runs on Render or another container host.
- The gateway authenticates with a Tailcat node key before the laptop accepts it.
- Each preview link contains a random access key and expires after four hours by default.
- HTTP streaming, server-sent events, and WebSocket upgrades pass through Go's reverse proxy.
- Registrations live in memory. The laptop restores them whenever the gateway restarts.

## How it works

```text
Android Chrome
      |
      | HTTPS
      v
Render gateway
      |
      | Tailcat, direct WireGuard when possible
      | DERP fallback otherwise
      v
devpreview expose 3000
      |
      | TCP to 127.0.0.1:3000
      v
Vite, Next.js, Rails, or another local dev server
```

The phone does not need an app. It only needs the preview URL printed by `devpreview`.

## Requirements

- Go 1.27 or Docker 20.10 or newer
- A local web development server
- A Render account for the free hosted gateway
- Outbound HTTPS and UDP access from the laptop

Tailcat Preview pins Tailcat while its API and wire format are still unstable. Upgrade that dependency deliberately and test both ends together.

## Try it locally

Create a control token. Both processes must use the same value.

```bash
export DEVPREVIEW_CONTROL_TOKEN="$(openssl rand -hex 32)"
```

Start the gateway:

```bash
export DEVPREVIEW_PUBLIC_URL=http://localhost:8080
export DEVPREVIEW_INSECURE_COOKIE=true
go run ./cmd/gateway
```

Start a development server in another terminal. This example uses Python only as a convenient HTTP server:

```bash
python3 -m http.server 3000
```

Expose it:

```bash
export DEVPREVIEW_GATEWAY_URL=http://localhost:8080
go run ./cmd/devpreview expose --name playground 3000
```

The command prints a link. Open it in a browser on the same machine for this local test. Stop the command to revoke the link immediately.

## Build without a local Go installation

The Docker build runs the tests and produces the gateway image:

```bash
docker build -t tailcat-preview-gateway .
```

Build both standalone binaries with BuildKit:

```bash
docker build --target binaries --output type=local,dest=./bin .
```

The output directory contains `gateway` and `devpreview`.

## Deploy the free gateway on Render

1. Put this directory in a GitHub repository.
2. Generate a control token with `openssl rand -hex 32` and keep it somewhere private.
3. In Render, create a Blueprint from `render.yaml`.
4. Enter the control token when Render asks for `DEVPREVIEW_CONTROL_TOKEN`.
5. Wait for the health check to pass.
6. Copy the service URL. It looks like `https://tailcat-preview-gateway.onrender.com`.

Render supplies that URL to the gateway through `RENDER_EXTERNAL_URL`, so no domain configuration is needed.

On the laptop:

```bash
export DEVPREVIEW_GATEWAY_URL=https://your-service.onrender.com
export DEVPREVIEW_CONTROL_TOKEN=the-same-control-token

./bin/devpreview expose --name my-app 3000
```

Send the printed link to your phone through Codex Remote, T3Connect, or another private channel.

## CLI options

```text
devpreview expose [options] <port>

  --gateway URL         Gateway URL. Defaults to DEVPREVIEW_GATEWAY_URL.
  --control-token TOKEN Shared token. Defaults to DEVPREVIEW_CONTROL_TOKEN.
  --name NAME           Label used in logs.
  --local-host HOST     Dev server host. Defaults to 127.0.0.1.
  --ttl DURATION        Link lifetime. Defaults to 4h, maximum 24h.
  --verbose             Print Tailcat networking logs.
```

## Security model

The control token authenticates the laptop to the gateway. Do not put it in source control or a preview URL.

When the laptop connects, the gateway sends its current Tailcat public key. The laptop allowlists that key before registering its Tailcat connection token. A party that discovers the Tailcat token cannot connect with a different key.

Every expose command creates:

- A new ephemeral Tailcat server identity
- A random preview ID
- A 256-bit browser access key
- An expiry time of no more than 24 hours

The gateway stores only a SHA-256 hash of the browser access key. Opening the link creates a signed, HTTP-only browser cookie and removes the key from subsequent URLs through a redirect. Stopping `devpreview` closes the Tailcat server and removes the gateway registration.

The gateway terminates HTTPS and can read proxied application traffic. Do not expose production secrets or applications whose contents the gateway operator must not see. A future Android direct mode can provide phone-to-laptop encryption without this trusted gateway.

## Current limits

- One expose process handles one local port.
- Registrations disappear while the laptop process is disconnected.
- Absolute redirects to localhost are rewritten, but unusual application-generated external URLs may still need configuration.
- Applications that require a fixed public hostname may reject the Render hostname.
- Tailcat's public DERP fallback is rate-limited and has no uptime promise.
- The gateway has not yet been load-tested against Render's 512 MB free instance.

## Next work

The next useful additions are a persistent laptop daemon, multiple previews per process, a preview dashboard, connection-path diagnostics, and an install script for the laptop binary.
