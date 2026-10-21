FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /dashboard .
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /dashboard /dashboard
USER 65532:65532
ENV LISTEN_ADDR=:8080 TASKS_FILE=/data/tasks.json
EXPOSE 8080
ENTRYPOINT ["/dashboard"]
