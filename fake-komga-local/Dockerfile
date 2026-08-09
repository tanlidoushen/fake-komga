FROM golang:1.26-alpine
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY . .
RUN go mod tidy && go build -o /fk-local ./cmd/server
ENTRYPOINT ["/fk-local"]
