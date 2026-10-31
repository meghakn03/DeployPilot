FROM golang:1.25.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /deploypilot ./cmd/deploypilot
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /deploypilot /deploypilot
USER 65532:65532
ENTRYPOINT ["/deploypilot"]
