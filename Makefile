.PHONY: test build validate compose-config

test:
	GOCACHE=/tmp/airipress-go-cache CGO_ENABLED=0 go test ./...
	python3 -m unittest discover -s tools/tests -v
	./deploy/install_test.sh
	@if test -d web/node_modules; then cd web && npm test -- --run && npm run typecheck; else echo "web/node_modules missing; run npm install in web"; exit 1; fi

build:
	mkdir -p bin
	GOCACHE=/tmp/airipress-go-cache CGO_ENABLED=0 go build -o bin/airipress ./cmd/airipress
	cd web && npm run build

validate:
	GOCACHE=/tmp/airipress-go-cache CGO_ENABLED=0 go test ./...
	python3 -m unittest discover -s tools/tests -v
	./deploy/install_test.sh
	@test -s api/openapi.yaml
	@bash -n deploy/install.sh

compose-config:
	docker compose --env-file .env.example -f compose.yaml -f deploy/compose.dev.yaml config >/dev/null
