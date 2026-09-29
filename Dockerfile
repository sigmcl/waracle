FROM golang:1.27.1-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/waracle-api ./cmd/api

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S waracle \
    && adduser -S -G waracle waracle

USER waracle
COPY --from=build /out/waracle-api /usr/local/bin/waracle-api

EXPOSE 8080
ENTRYPOINT ["waracle-api"]
