.PHONY: run test check rat qa-release release db-create db-migrate db-status quest-7-check
export NAME
run:
	go run ./backend/cmd/server
test:
	go -C backend test -race ./...
	npm test
check:
	go -C backend vet ./...
	npm run check
rat:
	go run ./backend/cmd/rat

db-create:
	go run ./backend/cmd/db create -dir database/migrations "$$NAME"

db-migrate:
	python3 scripts/dev.py migrate

db-status:
	python3 scripts/dev.py status

quest-7-check:
	python3 scripts/quest_7_check.py $(QUEST_ARGS)

qa-release:
	python3 scripts/qa.py $(QA_ARGS)

release:
	python3 scripts/release.py --output "$(OUTPUT)"
