# b2d-geo 서버·ETL 이미지 (멀티스테이지 → distroless 정적 바이너리).
#
#   빌드:        docker build -t b2d-geo:latest .
#   서버 실행:    docker run -e B2D_DATABASE_URL=postgres://user:pw@host:5432/b2d_geo -p 8080:8080 b2d-geo:latest
#   마이그레이션:  docker run --rm -e B2D_DATABASE_URL=... --entrypoint /b2d-etl b2d-geo:latest migrate
#   API키 발급:   docker run --rm -e B2D_DATABASE_URL=... --entrypoint /b2d-etl b2d-geo:latest apikey create --name x --scopes geo
#
# migrations는 go:embed로 바이너리에 내장되어 별도 COPY 불필요.
FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/b2d-server ./cmd/b2d-server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/b2d-etl ./cmd/b2d-etl && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/b2d-mcp ./cmd/b2d-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/b2d-server /b2d-server
COPY --from=build /out/b2d-etl /b2d-etl
COPY --from=build /out/b2d-mcp /b2d-mcp
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/b2d-server"]
