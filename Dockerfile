# docker run -e OPENROUTER_API_KEY="$OPENROUTER_API_KEY" -p 18790:18790 -it --volume="$PWD:/app" --workdir="/app" --entrypoint=/bin/sh clawlet
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o clawlet ./cmd/clawlet

FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/clawlet /usr/local/bin

RUN clawlet onboard
RUN clawlet status

EXPOSE 18790

ENTRYPOINT ["clawlet", "gateway"]
