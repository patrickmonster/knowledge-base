FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /knowledge-base ./cmd/knowledge-base

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 knowledge
WORKDIR /app
COPY --from=build /knowledge-base /app/knowledge-base
USER knowledge
EXPOSE 8080
CMD ["/app/knowledge-base", "serve"]
