FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /bot ./dev/bot

FROM alpine:3.22
COPY --from=build /bot /bot
ENTRYPOINT ["/bot"]
