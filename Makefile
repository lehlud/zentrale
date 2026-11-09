.PHONY: build test test-editor run dev generate format check race e2e

build:
	cd frontend && npm run build
	cd backend && go build -o zentrale .

run: build
	./backend/zentrale -frontend frontend/dist

dev:
	cd backend && go run . -frontend ../frontend/dist

test:
	cd backend && go test ./...
	cd backend && python3 -m unittest discover -s agent/scripts -p 'test_*.py'
	cd backend && python3 -m unittest discover -s remote/scripts -p 'test_*.py'
	cd backend && python3 -m unittest discover -s remote/microvm -p 'test_*.py'
	cd frontend && npm run typecheck

test-editor:
	cd backend && python3 -m unittest discover -s remote/scripts -p 'test_editor.py'

generate:
	cd backend && go run ./cmd/schema > ../frontend/schema.json
	cd frontend && npm run generate

format:
	cd backend && gofmt -w .
	cd frontend && npm run format

check:
	cd backend && go vet ./...
	cd frontend && npm run format:check

race:
	cd backend && go test -race ./...

e2e:
	cd frontend && npm run test:e2e
