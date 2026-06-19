.PHONY: proto
proto:
	@mkdir -p api/pb
	protoc --proto_path=api/proto \
	       --go_out=api/pb --go_opt=paths=source_relative \
	       --go-grpc_out=api/pb --go-grpc_opt=paths=source_relative \
	       api/proto/io_simulation.proto
	@echo "Proto code generated successfully!"

# ==============================================================================
# Frontend (gRPC-Web JavaScript)
# ==============================================================================

.PHONY: proto-js
proto-js:
	@mkdir -p web/src/generated
	protoc --proto_path=api/proto \
	       --js_out=import_style=commonjs:web/src/generated \
	       --grpc-web_out=import_style=commonjs,mode=grpcwebtext:web/src/generated \
	       api/proto/io_simulation.proto
	npx esbuild web/src/grpc-entry.js \
		--bundle --format=iife --global-name=IOSimBundle \
		--outfile=web/src/bundle.js --platform=browser
	@echo "gRPC-Web JS bundle generated!"

# ==============================================================================
# Docker Deployment Targets
# ==============================================================================

.PHONY: docker-build docker-up docker-down docker-logs
docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f