FROM golang:1.13.9-alpine3.11

WORKDIR /app

COPY . .

RUN export GO111MODULE=on
RUN go build -o main .

CMD ["/app/main"]
