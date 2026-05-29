# urban-lamp

An application that pings several services, stores results in SQLite, and displays live ping charts through SSR + SSE.

## Features

- HTTP GET and ICMP ping checks.
- Server-rendered HTML page.
- Live updates through Server-Sent Events.
- SQLite storage for services and ping history.
- Mobile and tablet friendly layout.
- Visual highlighting for slow responses and unavailable services.

## Run

```sh
go run .
```

Open http://localhost:8080.

The app uses the system `sqlite3` CLI for storage. On macOS it is available by default.

Configuration:

- `URBAN_LAMP_ADDR` changes listen address, default `:8080`.
- `PORT` changes listen port when `URBAN_LAMP_ADDR` is not set.
- `URBAN_LAMP_DB` changes SQLite database path, default `urban-lamp.db`.

## Docker

```sh
docker compose up --build
```

Compose stores SQLite data in the `urban-lamp-data` volume. Use `URBAN_LAMP_PORT=18080 docker compose up --build` to change the host port.
