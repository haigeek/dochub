.PHONY: test typecheck dev-server dev-web compose-up

test:
	cd server && go test ./...

typecheck:
	cd web && ./node_modules/.bin/vue-tsc --noEmit

dev-server:
	set -a; . ./.env; set +a; cd server && go run ./cmd/dochub

dev-web:
	cd web && pnpm dev

compose-up:
	docker compose up --build
