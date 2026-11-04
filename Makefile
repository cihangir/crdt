.PHONY: check fmt test fuzz bench

check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)
	go run ./internal/cmd/readmefmt
	go mod verify
	go vet ./...
	go test -race -shuffle=on -cover ./...

fmt:
	gofmt -w .
	go run ./internal/cmd/readmefmt -w

test:
	go test -race ./...

fuzz:
	go test -run '^$$' -fuzz '^FuzzCounterMerge$$' -fuzztime=15s
	go test -run '^$$' -fuzz '^FuzzSetConvergence$$' -fuzztime=15s
	go test -run '^$$' -fuzz '^FuzzRegisterConvergence$$' -fuzztime=15s

bench:
	go test -run '^$$' -bench . -benchmem ./...
