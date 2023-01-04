FROM golang:alpine as builder

WORKDIR /home/srv
COPY . .
RUN go mod tidy && go build  -o feishubot .

FROM golang:alpine
COPY --from=builder /home/srv/feishubot ./
CMD ["./feishubot"]