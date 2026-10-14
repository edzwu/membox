# membox-server all-in-one image: React home → embedded Go binary.
# Build from the REPO ROOT:  docker build -f deploy/membox-server/Dockerfile .

FROM node:22-alpine AS web
WORKDIR /src/internal/interfaces/httpserver/home
COPY internal/interfaces/httpserver/home/package.json internal/interfaces/httpserver/home/package-lock.json ./
RUN npm ci
COPY internal/interfaces/httpserver/home ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The vite output wins over any stale local static/.
COPY --from=web /src/internal/interfaces/httpserver/static internal/interfaces/httpserver/static
RUN CGO_ENABLED=0 go build -trimpath -o /out/membox-server ./cmd/membox-server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/membox-server /usr/local/bin/membox-server
EXPOSE 8081
ENTRYPOINT ["membox-server"]
