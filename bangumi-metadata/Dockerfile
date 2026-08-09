FROM golang:1.23-alpine AS builder
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /build
COPY . .
RUN go mod tidy && go build -o /build/bangumi-metadata ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/bangumi-metadata .
EXPOSE 25601
ENTRYPOINT ["/app/bangumi-metadata"]
CMD ["--db", "/data/fake-komga-115.db"]
