FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/urban-lamp .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates iputils sqlite \
	&& adduser -D -H -u 10001 app \
	&& mkdir -p /data \
	&& chown app:app /data

COPY --from=build /out/urban-lamp /usr/local/bin/urban-lamp

ENV PORT=8080
ENV URBAN_LAMP_DB=/data/urban-lamp.db

EXPOSE 8080
USER app

ENTRYPOINT ["/usr/local/bin/urban-lamp"]
