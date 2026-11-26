.PHONY: test vet windows run clean

BIN := dist/ntfy-notifier.exe

test:
	go test ./...

vet:
	go vet ./...

windows:
	mkdir -p dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -H=windowsgui" -o $(BIN) ./cmd/ntfy-notifier

run:
	go run ./cmd/ntfy-notifier --console

clean:
	rm -rf dist
