FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
COPY frontend/go.mod ./frontend/
COPY database/go.mod ./database/
RUN go -C backend mod download
COPY backend/cmd/server ./backend/cmd/server
COPY backend/cmd/db ./backend/cmd/db
COPY backend/internal ./backend/internal
COPY frontend ./frontend
COPY database ./database
RUN CGO_ENABLED=0 go -C backend build -trimpath -ldflags="-s -w" -o /rhythm ./cmd/server && CGO_ENABLED=0 go -C backend build -trimpath -ldflags="-s -w" -o /rhythm-db ./cmd/db && mkdir /data && chmod 700 /data

FROM scratch
COPY --from=build /rhythm /rhythm
COPY --from=build /rhythm-db /rhythm-db
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /data /data
USER 10001:10001
ENV ADDR=:8080 DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/rhythm"]
