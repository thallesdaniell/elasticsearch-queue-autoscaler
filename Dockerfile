FROM golang:1.27.3 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /manager ./cmd
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
