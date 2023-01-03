FROM golang:alpine as builder

WORKDIR /home/srv
COPY . .
RUN go mod init proxy && go mod tidy && go build  -o proxy .

FROM golang:alpine
COPY --from=builder /home/srv/proxy ./
CMD ["./proxy"]