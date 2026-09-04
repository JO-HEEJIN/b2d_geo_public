.PHONY: up down build test vet migrate psql docker-build

up:
	docker compose up -d --wait

down:
	docker compose down

build:
	go build -o bin/b2d-server ./cmd/b2d-server
	go build -o bin/b2d-etl ./cmd/b2d-etl
	go build -o bin/b2d-mcp ./cmd/b2d-mcp

test:
	go test ./...

vet:
	go vet ./...

migrate:
	go run ./cmd/b2d-etl migrate

psql:
	docker compose exec postgis psql -U b2d -d b2d_geo

docker-build:
	docker build -t b2d-geo:latest .
