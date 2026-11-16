FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
# Railway passes the deployed commit as a build argument; /version and
# get_server_version fall back to the runtime variable when it is empty.
ARG RAILWAY_GIT_COMMIT_SHA=""
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X knowledge-base/internal/kb.BuildCommit=${RAILWAY_GIT_COMMIT_SHA}" -o /knowledge-base ./cmd/knowledge-base

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 knowledge
WORKDIR /app
COPY --from=build /knowledge-base /app/knowledge-base
USER knowledge
EXPOSE 8080
CMD ["/app/knowledge-base", "serve"]
