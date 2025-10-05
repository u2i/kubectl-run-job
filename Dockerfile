FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod ./
COPY main.go ./
RUN go mod tidy
# Force rebuild - bypassing Go build cache
RUN CGO_ENABLED=0 go build -a -o kubectl-run-job .

FROM alpine:latest

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/kubectl-run-job /usr/local/bin/kubectl-run-job

ENTRYPOINT ["/usr/local/bin/kubectl-run-job"]
