# build
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/captain ./cmd/server

# run
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl
COPY --from=build /out/captain /usr/local/bin/captain
EXPOSE 8080
ENTRYPOINT ["captain"]
