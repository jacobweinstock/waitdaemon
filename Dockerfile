FROM golang:1.21 as builder

WORKDIR /code
COPY go.mod go.sum /code/
RUN go mod download

COPY . /code

RUN CGO_ENABLED=0 go build -o /waitdaemon main.go

FROM scratch
COPY --from=builder /waitdaemon /waitdaemon

ENTRYPOINT [ "/waitdaemon" ]
