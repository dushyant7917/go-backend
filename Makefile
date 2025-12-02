.PHONY: help dev build run docker-up docker-down migrate-up migrate-down migrate-create clean

help: ## Display this help message
	@echo "Available commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

dev: ## Run the application in development mode
	@echo "Starting development server..."
	GIN_MODE=debug go run cmd/server/main.go

build: ## Build the application
	@echo "Building application..."
	go build -o bin/server cmd/server/main.go

run: build ## Build and run the application
	@echo "Running application..."
	./bin/server

docker-up: ## Start PostgreSQL database with Docker Compose
	@echo "Starting PostgreSQL database..."
	docker-compose up -d

docker-down: ## Stop PostgreSQL database
	@echo "Stopping PostgreSQL database..."
	docker-compose down

docker-clean: ## Stop and remove PostgreSQL database with volumes
	@echo "Cleaning PostgreSQL database..."
	docker-compose down -v

migrate-up: ## Run all pending migrations
	@echo "Running migrations..."
	goose -dir migrations postgres "host=localhost port=5432 user=postgres password=postgres dbname=go_backend sslmode=disable" up

migrate-down: ## Rollback the last migration
	@echo "Rolling back migration..."
	goose -dir migrations postgres "host=localhost port=5432 user=postgres password=postgres dbname=go_backend sslmode=disable" down

migrate-status: ## Check migration status
	@echo "Migration status..."
	goose -dir migrations postgres "host=localhost port=5432 user=postgres password=postgres dbname=go_backend sslmode=disable" status

migrate-create: ## Create a new migration file (usage: make migrate-create NAME=migration_name)
	@if [ -z "$(NAME)" ]; then \
		echo "Error: NAME is required. Usage: make migrate-create NAME=migration_name"; \
		exit 1; \
	fi
	@echo "Creating migration: $(NAME)..."
	goose -dir migrations create $(NAME) sql

test: ## Run tests
	@echo "Running tests..."
	go test -v ./...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	go clean

deps: ## Install/update dependencies
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

setup: docker-up migrate-up ## Setup development environment (start DB and run migrations)
	@echo "Development environment setup complete!"
