FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod ./
COPY main.go ./
RUN go mod tidy
RUN CGO_ENABLED=0 go build -o kubectl-run-job .

FROM alpine:latest

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/kubectl-run-job /usr/local/bin/kubectl-run-job

ENTRYPOINT ["/usr/local/bin/kubectl-run-job"]
