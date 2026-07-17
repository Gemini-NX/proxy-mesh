FROM golang:1.24-alpine
WORKDIR /src
COPY go.mod main.go ./
RUN go mod tidy
RUN go build -o /library-smoke .
ENTRYPOINT ["/library-smoke"]
