# --- build stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./src

# --- runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /root/

COPY --from=builder /app/server .

EXPOSE 8080

ENV GIN_MODE=release
ENV OLLAMA_URL=http://host.docker.internal:11434/api/chat
ENV MODEL=qwen2.5:0.5b

CMD ["./server"]
