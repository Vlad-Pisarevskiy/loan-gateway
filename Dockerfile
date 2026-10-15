# Сборка обоих бинарников; финальный образ — distroless без shell.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockbank ./cmd/mockbank

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/gateway /out/mockbank /usr/local/bin/
USER nonroot:nonroot
